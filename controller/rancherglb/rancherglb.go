package rancherglb

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/leodotcloud/log"
	"github.com/patrickmn/go-cache"
	"github.com/rancher/go-rancher-metadata/metadata"
	"github.com/rancher/go-rancher/v2"
	"github.com/rancher/lb-controller/config"
	"github.com/rancher/lb-controller/controller"
	"github.com/rancher/lb-controller/controller/rancher"
	"github.com/rancher/lb-controller/provider"
	utils "github.com/rancher/lb-controller/utils"
)

func init() {
	lbc, err := newGLBController()
	if err != nil {
		log.Fatalf("%v", err)
	}

	controller.RegisterController(lbc.GetName(), lbc)
}

func (lbc *glbController) Init(metadataURL string) {
	lbc.rancherController.Init(metadataURL)

	metadataClient, err := metadata.NewClientAndWait(metadataURL)
	if err != nil {
		log.Fatalf("Error initiating metadata client: %v", err)
	}

	lbc.metaFetcher = rancher.RMetaFetcher{
		MetadataClient: metadataClient,
	}

	lbc.rancherController.MetaFetcher = rancher.RMetaFetcher{
		MetadataClient: metadataClient,
	}
}

type glbController struct {
	shutdown                   bool
	stopCh                     chan struct{}
	lbProvider                 provider.LBProvider
	syncQueue                  *utils.TaskQueue
	opts                       *client.ClientOpts
	incrementalBackoff         int64
	incrementalBackoffInterval int64
	metaFetcher                MetadataFetcher
	rancherController          *rancher.LoadBalancerController
	endpointsCache             *cache.Cache
}

type MetadataFetcher interface {
	GetSelfService() (metadata.Service, error)
	GetService(link string) (*metadata.Service, error)
	OnChange(intervalSeconds int, do func(string))
	GetServices() ([]metadata.Service, error)
	GetRegionName() (string, error)
}

func (lbc *glbController) GetName() string {
	return "rancherglb"
}

func newGLBController() (*glbController, error) {
	lbc, err := rancher.NewLoadBalancerController()
	if err != nil {
		return nil, err
	}

	c := cache.New(1*time.Hour, 1*time.Minute)

	glb := &glbController{
		stopCh:                     make(chan struct{}),
		incrementalBackoff:         0,
		incrementalBackoffInterval: 5,
		rancherController:          lbc,
		endpointsCache:             c,
	}
	glb.syncQueue = utils.NewTaskQueue(glb.sync)

	return glb, nil
}

func (lbc *glbController) sync(key string) {
	if lbc.shutdown {
		//skip syncing if controller is being shut down
		return
	}
	log.Debugf("Syncing up LB")
	requeue := false
	cfgs, err := lbc.GetLBConfigs()
	if err == nil {
		for _, cfg := range cfgs {
			if err := lbc.lbProvider.ApplyConfig(cfg); err != nil {
				log.Errorf("Failed to apply lb config on provider: %v", err)
				requeue = true
			}
		}
	} else {
		log.Errorf("Failed to get lb config: %v", err)
		requeue = true
	}

	if requeue {
		go lbc.requeue(key)
	} else {
		//clear up the backoff
		lbc.incrementalBackoff = 0
	}
}

func (lbc *glbController) GetLBConfigs() ([]*config.LoadBalancerConfig, error) {
	glbSvc, err := lbc.metaFetcher.GetSelfService()
	if err != nil {
		return nil, err
	}
	return lbc.GetGLBConfigs(glbSvc)
}

func (lbc *glbController) GetGLBConfigs(glbSvc metadata.Service) ([]*config.LoadBalancerConfig, error) {
	glbMeta, err := rancher.GetLBMetadata(glbSvc.LBConfig)
	if err != nil {
		return nil, err
	}

	var lbSvcs []metadata.Service
	svcs, err := lbc.metaFetcher.GetServices()
	if err != nil {
		return nil, err
	}

	for _, svc := range svcs {
		if !strings.EqualFold(svc.Kind, "loadBalancerService") {
			continue
		}
		lbSvcs = append(lbSvcs, svc)
	}

	//for every lb service, get metadata and build the config
	var configs []*config.LoadBalancerConfig
	for _, glbRule := range glbMeta.PortRules {
		for _, lbSvc := range lbSvcs {
			if !rancher.IsSelectorMatch(glbRule.Selector, lbSvc.Labels) {
				continue
			}

			if !rancher.IsActiveService(&lbSvc) {
				// cleanup public endpoints
				eps := []client.PublicEndpoint{}
				log.Debugf("cleaning up endpoints for inactive lb uuid [%v]", lbSvc.UUID)
				if err := lbc.updateEndpoints(&lbSvc, eps); err != nil {
					return nil, err
				}
				continue
			}
			lbConfig := lbSvc.LBConfig
			if len(lbConfig.PortRules) == 0 {
				continue
			}
			sourcePort := glbRule.SourcePort
			proto := glbRule.Protocol
			lbMeta, err := lbc.rancherController.CollectLBMetadata(lbSvc)
			if err != nil {
				return nil, err
			}
			cs, err := lbc.rancherController.BuildConfigFromMetadata(lbSvc.Name, lbSvc.EnvironmentUUID, "", "any", lbMeta)
			if err != nil {
				return nil, err
			}

			//source port and proto from the glb
			for _, cs := range cs {
				for _, fe := range cs.FrontendServices {
					fe.Port = sourcePort
					fe.Protocol = proto
				}
			}

			configs = append(configs, cs...)

			//update endpoints
			eps, err := getEndpoints(&glbSvc, sourcePort)
			if err != nil {
				return nil, err
			}
			if err := lbc.updateEndpoints(&lbSvc, eps); err != nil {
				return nil, err
			}
		}
	}

	merged, err := lbc.mergeConfigs(glbSvc, configs)
	if err != nil {
		return nil, err
	}
	for _, config := range merged {
		if err = lbc.lbProvider.ProcessCustomConfig(config, glbMeta.Config); err != nil {
			return nil, err
		}
	}
	return merged, nil
}

func (lbc *glbController) needEndpointsUpdate(lbSvc *metadata.Service, eps []client.PublicEndpoint) bool {
	previousEps, _ := lbc.endpointsCache.Get(lbSvc.UUID)
	log.Debugf("previous endpoints are %v", previousEps)
	return !reflect.DeepEqual(previousEps, eps)
}

func (lbc *glbController) updateEndpoints(lbSvc *metadata.Service, eps []client.PublicEndpoint) error {
	log.Debugf("endpoints are %v", eps)
	if !lbc.needEndpointsUpdate(lbSvc, eps) {
		log.Debug("no need to update endpoints")
		return nil
	}
	if err := lbc.rancherController.CertFetcher.UpdateEndpoints(lbSvc, eps); err != nil {
		return err
	}
	lbc.endpointsCache.Set(lbSvc.UUID, eps, cache.DefaultExpiration)
	return nil
}

func getEndpoints(glbSvc *metadata.Service, lbPort int) ([]client.PublicEndpoint, error) {
	var publicEndpoints []client.PublicEndpoint
	for _, c := range glbSvc.Containers {
		for _, port := range c.Ports {
			splitted := strings.Split(port, ":")
			port, err := strconv.Atoi(splitted[1])
			if err != nil {
				return nil, err
			}
			if port != lbPort {
				log.Infof("ports are diff, %v and %v", port, lbPort)
				continue
			}
			pE := client.PublicEndpoint{
				IpAddress: splitted[0],
				Port:      int64(port),
			}
			publicEndpoints = append(publicEndpoints, pE)
		}
	}
	return publicEndpoints, nil
}

/*
merge host name routing rules from all the configs
*/
func (lbc *glbController) mergeConfigs(glbSvc metadata.Service, configs []*config.LoadBalancerConfig) ([]*config.LoadBalancerConfig, error) {
	var merged []*config.LoadBalancerConfig
	frontendsMap := map[string]*config.FrontendService{}
	// 1. merge frontends and their backends
	for _, lbConfig := range configs {
		for _, fe := range lbConfig.FrontendServices {
			name := strconv.Itoa(fe.Port)
			if val, ok := frontendsMap[name]; ok {
				existing := val
				existing.BackendServices = append(existing.BackendServices, fe.BackendServices...)
			} else {
				frontendsMap[name] = fe
			}
		}
	}

	// 2. merge backend services
	for _, fe := range frontendsMap {
		bes := map[string]*config.BackendService{}
		for _, be := range fe.BackendServices {
			pathUUID := fmt.Sprintf("%v_%s_%s_%s", fe.Port, be.Host, be.Path, be.RuleComparator)
			existing := bes[pathUUID]
			if existing == nil {
				existing = be
			} else {
				existing.Endpoints = append(existing.Endpoints, be.Endpoints...)
			}
			bes[pathUUID] = existing
		}
		backends := []*config.BackendService{}
		for _, be := range bes {
			backends = append(backends, be)
		}
		fe.BackendServices = backends
	}

	// 3. sort frontends and backends
	var frontends config.FrontendServices
	for _, v := range frontendsMap {
		// sort endpoints
		for _, b := range v.BackendServices {
			sort.Sort(b.Endpoints)
		}
		// sort backends
		sort.Sort(v.BackendServices)
		frontends = append(frontends, v)
	}
	//sort frontends
	sort.Sort(frontends)

	//get glb info
	glbMeta, err := lbc.rancherController.CollectLBMetadata(glbSvc)
	if err != nil {
		return nil, err
	}
	certs := []*config.Certificate{}
	var defaultCert *config.Certificate

	defCerts, err := lbc.rancherController.CertFetcher.FetchCertificates(glbMeta, true)
	if err != nil {
		return nil, err
	}
	if len(defCerts) > 0 {
		defaultCert = defCerts[0]
		if defaultCert != nil {
			certs = append(certs, defaultCert)
		}
	}

	alternateCerts, err := lbc.rancherController.CertFetcher.FetchCertificates(glbMeta, false)
	if err != nil {
		return nil, err
	}
	certs = append(certs, alternateCerts...)

	lbConfig := &config.LoadBalancerConfig{
		Name:             glbSvc.Name,
		FrontendServices: frontends,
		Certs:            certs,
		DefaultCert:      defaultCert,
		StickinessPolicy: &glbMeta.StickinessPolicy,
	}

	merged = append(merged, lbConfig)

	return merged, nil
}

func (lbc *glbController) Run(provider provider.LBProvider) {
	log.Infof("starting %s controller", lbc.GetName())
	lbc.lbProvider = provider
	lbc.rancherController.LBProvider = provider
	go lbc.syncQueue.Run(time.Second, lbc.stopCh)

	go lbc.lbProvider.Run(nil)

	lbc.metaFetcher.OnChange(5, lbc.ScheduleApplyConfig)

	<-lbc.stopCh
}

func (lbc *glbController) Stop() error {
	if !lbc.shutdown {
		log.Infof("Shutting down %s controller", lbc.GetName())
		//stop the provider
		if err := lbc.lbProvider.Stop(); err != nil {
			return err
		}
		close(lbc.stopCh)
		lbc.shutdown = true
	}

	return fmt.Errorf("shutdown already in progress")
}

func (lbc *glbController) IsHealthy() bool {
	return true
}

func (lbc *glbController) ScheduleApplyConfig(string) {
	log.Debug("Scheduling apply config")
	lbc.syncQueue.Enqueue(lbc.GetName())
}

func (lbc *glbController) requeue(key string) {
	// requeue only when after incremental backoff time
	lbc.incrementalBackoff = lbc.incrementalBackoff + lbc.incrementalBackoffInterval
	time.Sleep(time.Duration(lbc.incrementalBackoff) * time.Second)
	lbc.syncQueue.Requeue(key, fmt.Errorf("retrying sync as one of the configs failed to apply on a backend"))
}
