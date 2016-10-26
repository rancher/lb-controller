package rancher

import (
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/rancher/go-rancher-metadata/metadata"
	"github.com/rancher/go-rancher/client"
	"github.com/rancher/lb-controller/config"
	"github.com/rancher/lb-controller/controller"
	"github.com/rancher/lb-controller/provider"
	utils "github.com/rancher/lb-controller/utils"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	metadataURL = "http://rancher-metadata/2015-12-19"
)

func init() {
	lbc, err := newLoadBalancerController()
	if err != nil {
		logrus.Fatalf("%v", err)
	}

	controller.RegisterController(lbc.GetName(), lbc)
}

func (lbc *loadBalancerController) Init() {
	cattleURL := os.Getenv("CATTLE_URL")
	if len(cattleURL) == 0 {
		logrus.Fatalf("CATTLE_URL is not set, fail to init Rancher LB provider")
	}

	cattleAccessKey := os.Getenv("CATTLE_ACCESS_KEY")
	if len(cattleAccessKey) == 0 {
		logrus.Fatalf("CATTLE_ACCESS_KEY is not set, fail to init of Rancher LB provider")
	}

	cattleSecretKey := os.Getenv("CATTLE_SECRET_KEY")
	if len(cattleSecretKey) == 0 {
		logrus.Fatalf("CATTLE_SECRET_KEY is not set, fail to init of Rancher LB provider")
	}

	opts := &client.ClientOpts{
		Url:       cattleURL,
		AccessKey: cattleAccessKey,
		SecretKey: cattleSecretKey,
	}

	client, err := client.NewRancherClient(opts)
	if err != nil {
		logrus.Fatalf("Failed to create Rancher client %v", err)
	}

	certFetcher := &rCertificateFetcher{
		client: client,
	}

	lbc.certFetcher = certFetcher
}

type loadBalancerController struct {
	shutdown                   bool
	stopCh                     chan struct{}
	lbProvider                 provider.LBProvider
	syncQueue                  *utils.TaskQueue
	opts                       *client.ClientOpts
	incrementalBackoff         int64
	incrementalBackoffInterval int64
	certFetcher                CertificateFetcher
	metaFetcher                MetadataFetcher
}

type MetadataFetcher interface {
	GetSelfService() (metadata.Service, error)
	GetService(svcName string, stackName string) (*metadata.Service, error)
	OnChange(intervalSeconds int, do func(string))
	GetServices() ([]metadata.Service, error)
}

type rMetaFetcher struct {
	metadataClient metadata.Client
}

type CertificateFetcher interface {
	fetchCertificate(certName string) (*config.Certificate, error)
}

type rCertificateFetcher struct {
	client *client.RancherClient
}

func (lbc *loadBalancerController) GetName() string {
	return "rancher"
}

func (lbc *loadBalancerController) Run(provider provider.LBProvider) {
	logrus.Infof("starting %s controller", lbc.GetName())
	lbc.lbProvider = provider
	go lbc.syncQueue.Run(time.Second, lbc.stopCh)

	go lbc.lbProvider.Run(nil)

	metadataClient, err := metadata.NewClientAndWait(metadataURL)
	if err != nil {
		logrus.Errorf("Error initiating metadata client: %v", err)
		lbc.Stop()
	}

	lbc.metaFetcher = rMetaFetcher{
		metadataClient: metadataClient,
	}

	lbc.metaFetcher.OnChange(5, lbc.ScheduleApplyConfig)

	<-lbc.stopCh
}

func (mf rMetaFetcher) OnChange(intervalSeconds int, do func(string)) {
	mf.metadataClient.OnChange(intervalSeconds, do)
}

func (lbc *loadBalancerController) ScheduleApplyConfig(string) {
	logrus.Debug("Scheduling apply config")
	lbc.syncQueue.Enqueue(lbc.GetName())
}

func (lbc *loadBalancerController) Stop() error {
	if !lbc.shutdown {
		logrus.Infof("Shutting down %s controller", lbc.GetName())
		//stop the provider
		if err := lbc.lbProvider.Stop(); err != nil {
			return err
		}
		close(lbc.stopCh)
		lbc.shutdown = true
	}

	return fmt.Errorf("shutdown already in progress")
}

func (lbc *loadBalancerController) BuildConfigFromMetadata(lbName string, lbMeta *LBMetadata) ([]*config.LoadBalancerConfig, error) {
	lbConfigs := []*config.LoadBalancerConfig{}
	if lbMeta == nil {
		lbMeta = &LBMetadata{
			PortRules:   make([]metadata.PortRule, 0),
			Certs:       make([]string, 0),
			DefaultCert: "",
			Config:      "",
		}
	}
	frontendsMap := map[string]*config.FrontendService{}
	// fetch certificates
	certs := []*config.Certificate{}
	for _, certName := range lbMeta.Certs {
		cert, err := lbc.certFetcher.fetchCertificate(certName)
		if err != nil {
			return nil, err
		}
		certs = append(certs, cert)
	}
	defaultCert, err := lbc.certFetcher.fetchCertificate(lbMeta.DefaultCert)
	if err != nil {
		return nil, err
	}

	if defaultCert != nil {
		certs = append(certs, defaultCert)
	}
	allBe := make(map[string]*config.BackendService)
	allEps := make(map[string]map[string]string)
	reg, err := regexp.Compile("[^A-Za-z0-9]+")
	if err != nil {
		return nil, err
	}
	for _, rule := range lbMeta.PortRules {
		if rule.SourcePort < 1 {
			continue
		}
		var frontend *config.FrontendService
		name := strconv.Itoa(rule.SourcePort)
		if val, ok := frontendsMap[name]; ok {
			frontend = val
		} else {
			backends := []*config.BackendService{}
			frontend = &config.FrontendService{
				Name:            name,
				Port:            rule.SourcePort,
				Protocol:        rule.Protocol,
				BackendServices: backends,
			}
		}
		// service comes in a format of stackName/serviceName,
		// replace "/"" with "_"
		svcName := strings.SplitN(rule.Service, "/", 2)
		service, err := lbc.metaFetcher.GetService(svcName[1], svcName[0])
		if err != nil {
			return nil, err
		}
		eps, err := lbc.getServiceEndpoints(service, rule.TargetPort, true)
		if err != nil {
			return nil, err
		}

		hc, err := getServiceHealthCheck(service)
		if err != nil {
			return nil, err
		}

		comparator := config.EqRuleComparator
		path := rule.Path
		hostname := rule.Hostname
		if !(strings.EqualFold(rule.Protocol, config.HTTPSProto) || strings.EqualFold(rule.Protocol, config.HTTPProto) || strings.EqualFold(rule.Protocol, config.SNIProto)) {
			path = ""
			hostname = ""
		}

		if len(hostname) > 2 {
			if strings.HasPrefix(hostname, "*.") {
				hostname = hostname[2:len(hostname)]
				comparator = config.EndRuleComparator
			} else if strings.HasSuffix(hostname, ".*") {
				hostname = hostname[:len(hostname)-2]
				comparator = config.BegRuleComparator
			}
		}

		pathUUID := fmt.Sprintf("%v_%s_%s", rule.SourcePort, hostname, path)
		backend := allBe[pathUUID]
		if backend != nil {
			epMap := allEps[pathUUID]
			for _, ep := range eps {
				if _, ok := epMap[ep.IP]; !ok {
					epMap[ep.IP] = ep.IP
					backend.Endpoints = append(backend.Endpoints, ep)
				}
			}
		} else {
			UUID := rule.BackendName
			if UUID == "" {
				//replace all non alphanumeric with _
				UUID = reg.ReplaceAllString(pathUUID, "_")
			}
			backend := &config.BackendService{
				UUID:           UUID,
				Host:           hostname,
				Path:           path,
				Port:           rule.TargetPort,
				Protocol:       rule.Protocol,
				RuleComparator: comparator,
				Endpoints:      eps,
				HealthCheck:    hc,
				Priority:       rule.Priority,
			}
			allBe[pathUUID] = backend
			frontend.BackendServices = append(frontend.BackendServices, backend)
			epMap := make(map[string]string)
			for _, ep := range eps {
				epMap[ep.IP] = ep.IP
			}
			allEps[pathUUID] = epMap
		}

		frontendsMap[name] = frontend
	}

	var frontends config.FrontendServices
	for _, v := range frontendsMap {
		// sort backends
		sort.Sort(v.BackendServices)
		frontends = append(frontends, v)
	}

	//sort frontends
	sort.Sort(frontends)

	lbConfig := &config.LoadBalancerConfig{
		Name:             lbName,
		FrontendServices: frontends,
		Certs:            certs,
		DefaultCert:      defaultCert,
		StickinessPolicy: &lbMeta.StickinessPolicy,
	}

	if err = lbc.lbProvider.ProcessCustomConfig(lbConfig, lbMeta.Config); err != nil {
		return nil, err
	}

	lbConfigs = append(lbConfigs, lbConfig)

	return lbConfigs, nil
}

func (mf rMetaFetcher) GetSelfService() (metadata.Service, error) {
	return mf.metadataClient.GetSelfService()
}

func (lbc *loadBalancerController) GetLBConfigs() ([]*config.LoadBalancerConfig, error) {
	lbSvc, err := lbc.metaFetcher.GetSelfService()
	if err != nil {
		return nil, err
	}

	lbMeta, err := lbc.collectLBMetadata(lbSvc)
	if err != nil {
		return nil, err
	}

	return lbc.BuildConfigFromMetadata(lbSvc.Name, lbMeta)
}

func (lbc *loadBalancerController) collectLBMetadata(lbSvc metadata.Service) (*LBMetadata, error) {
	lbConfig := lbSvc.LBConfig
	if len(lbConfig.PortRules) == 0 {
		logrus.Debugf("Metadata is empty for the service %v", lbSvc.Name)
		return nil, nil
	}

	lbMeta, err := getLBMetadata(lbConfig)
	if err != nil {
		return nil, err
	}

	if err = lbc.processSelector(lbMeta); err != nil {
		return nil, err
	}

	return lbMeta, nil
}

func (lbc *loadBalancerController) processSelector(lbMeta *LBMetadata) error {
	//collect selector based services
	var rules []metadata.PortRule
	svcs, err := lbc.metaFetcher.GetServices()
	if err != nil {
		return err
	}
	for _, lbRule := range lbMeta.PortRules {
		if lbRule.Selector == "" {
			rules = append(rules, lbRule)
			continue
		}
		for _, svc := range svcs {
			if !IsSelectorMatch(lbRule.Selector, svc.Labels) {
				continue
			}
			lbConfig := svc.LBConfig
			if len(lbConfig.PortRules) == 0 {
				continue
			}

			meta, err := getLBMetadata(lbConfig)
			if err != nil {
				return err
			}
			for _, rule := range meta.PortRules {
				port := metadata.PortRule{
					SourcePort:  lbRule.SourcePort,
					Protocol:    lbRule.Protocol,
					Path:        rule.Path,
					Hostname:    rule.Hostname,
					Service:     rule.Service,
					TargetPort:  rule.TargetPort,
					BackendName: rule.BackendName,
				}
				rules = append(rules, port)
			}
		}
	}

	lbMeta.PortRules = rules
	return nil
}

func (fetcher *rCertificateFetcher) fetchCertificate(certName string) (*config.Certificate, error) {
	if certName == "" {
		return nil, nil
	}
	opts := client.NewListOpts()
	opts.Filters["name"] = certName
	opts.Filters["removed_null"] = "1"

	certs, err := fetcher.client.Certificate.List(opts)
	if err != nil {
		return nil, fmt.Errorf("Coudln't get certificate by name [%s]. Error: %#v", certName, err)
	}
	var cert client.Certificate
	var certWithChain string
	if len(certs.Data) >= 1 {
		cert = certs.Data[0]
		certWithChain = fmt.Sprintf("%s\n%s", cert.Cert, cert.CertChain)
	}

	return &config.Certificate{
		Name: cert.Name,
		Key:  cert.Key,
		Cert: certWithChain,
	}, nil
}

func getServiceHealthCheck(svc *metadata.Service) (*config.HealthCheck, error) {
	if &svc.HealthCheck == nil {
		return nil, nil
	}
	return getConfigServiceHealthCheck(svc.HealthCheck)
}

func (mf rMetaFetcher) GetServices() ([]metadata.Service, error) {
	return mf.metadataClient.GetServices()
}

func (mf rMetaFetcher) GetService(svcName string, stackName string) (*metadata.Service, error) {
	svcs, err := mf.metadataClient.GetServices()
	if err != nil {
		return nil, err
	}
	var service metadata.Service
	for _, svc := range svcs {
		if strings.EqualFold(svc.Name, svcName) && strings.EqualFold(svc.StackName, stackName) {
			service = svc
			break
		}
	}
	return &service, nil
}

func (lbc *loadBalancerController) getServiceEndpoints(svc *metadata.Service, targetPort int, activeOnly bool) (config.Endpoints, error) {
	var eps config.Endpoints
	var err error
	if strings.EqualFold(svc.Kind, "service") {
		eps = lbc.getRegularServiceEndpoints(svc, targetPort, activeOnly)
	} else if strings.EqualFold(svc.Kind, "externalService") {
		eps = lbc.getExternalServiceEndpoints(svc, targetPort)
	} else if strings.EqualFold(svc.Kind, "dnsService") {
		eps, err = lbc.getAliasServiceEndpoints(svc, targetPort, activeOnly)
		if err != nil {
			return nil, err
		}
	}

	// sort endpoints
	sort.Sort(eps)
	return eps, nil
}

func (lbc *loadBalancerController) getAliasServiceEndpoints(svc *metadata.Service, targetPort int, activeOnly bool) (config.Endpoints, error) {
	var eps config.Endpoints
	for link := range svc.Links {
		svcName := strings.SplitN(link, "/", 2)
		service, err := lbc.metaFetcher.GetService(svcName[1], svcName[0])
		if err != nil {
			return nil, err
		}
		if service == nil {
			continue
		}
		newEps, err := lbc.getServiceEndpoints(service, targetPort, activeOnly)
		if err != nil {
			return nil, err
		}
		eps = append(eps, newEps...)
	}
	return eps, nil
}

func (lbc *loadBalancerController) getExternalServiceEndpoints(svc *metadata.Service, targetPort int) config.Endpoints {
	var eps config.Endpoints
	for _, e := range svc.ExternalIps {
		ep := &config.Endpoint{
			Name: e,
			IP:   e,
			Port: targetPort,
		}
		eps = append(eps, ep)
	}
	return eps
}

func (lbc *loadBalancerController) getRegularServiceEndpoints(svc *metadata.Service, targetPort int, activeOnly bool) config.Endpoints {
	var eps config.Endpoints
	for _, c := range svc.Containers {
		if strings.EqualFold(c.State, "running") || strings.EqualFold(c.State, "starting") {
			ep := &config.Endpoint{
				Name: c.PrimaryIp,
				IP:   c.PrimaryIp,
				Port: targetPort,
			}
			eps = append(eps, ep)
		}
	}
	return eps
}

func (lbc *loadBalancerController) IsHealthy() bool {
	return true
}

func newLoadBalancerController() (*loadBalancerController, error) {
	lbc := &loadBalancerController{
		stopCh:                     make(chan struct{}),
		incrementalBackoff:         0,
		incrementalBackoffInterval: 5,
	}
	lbc.syncQueue = utils.NewTaskQueue(lbc.sync)

	return lbc, nil
}

func (lbc *loadBalancerController) sync(key string) {
	if lbc.shutdown {
		//skip syncing if controller is being shut down
		return
	}
	logrus.Info("Syncing up LB")
	requeue := false
	cfgs, err := lbc.GetLBConfigs()
	if err == nil {
		for _, cfg := range cfgs {
			if err := lbc.lbProvider.ApplyConfig(cfg); err != nil {
				logrus.Errorf("Failed to apply lb config on provider: %v", err)
				requeue = true
			}
		}
	} else {
		logrus.Errorf("Failed to get lb config: %v", err)
		requeue = true
	}

	if requeue {
		go lbc.requeue(key)
	} else {
		//clear up the backoff
		lbc.incrementalBackoff = 0
	}
}

func (lbc *loadBalancerController) requeue(key string) {
	// requeue only when after incremental backoff time
	lbc.incrementalBackoff = lbc.incrementalBackoff + lbc.incrementalBackoffInterval
	time.Sleep(time.Duration(lbc.incrementalBackoff) * time.Second)
	lbc.syncQueue.Requeue(key, fmt.Errorf("retrying sync as one of the configs failed to apply on a backend"))
}
