package rancherglb

import (
	// "github.com/Sirupsen/logrus"
	"github.com/patrickmn/go-cache"
	"github.com/rancher/go-rancher-metadata/metadata"
	"github.com/rancher/go-rancher/v2"
	"github.com/rancher/lb-controller/config"
	"github.com/rancher/lb-controller/controller/rancher"
	utils "github.com/rancher/lb-controller/utils"
	"strings"
	"testing"
	"time"
)

var glb *glbController

func init() {
	lbc := &rancher.LoadBalancerController{
		MetaFetcher: tMetaFetcher{},
		CertFetcher: tCertFetcher{},
		LBProvider:  &tProvider{},
	}

	glb = &glbController{
		stopCh:                     make(chan struct{}),
		incrementalBackoff:         0,
		incrementalBackoffInterval: 5,
		rancherController:          lbc,
		metaFetcher:                tMetaFetcher{},
		lbProvider:                 &tProvider{},
		endpointsCache:             cache.New(1*time.Hour, 1*time.Minute),
	}
}

type tProvider struct {
}

type tCertFetcher struct {
}

type tMetaFetcher struct {
}

func TestBasicCaseTwoServices(t *testing.T) {
	configs, err := glb.GetLBConfigs()
	if err != nil {
		t.Fatalf("Error getting configs: %s", err)
	}

	if len(configs) != 1 {
		t.Fatalf("Incorrect number of configs, expected 1, actual: %v", len(configs))
	}

	fes := configs[0].FrontendServices
	if len(fes) != 1 {
		t.Fatalf("Incorrect number of frontends, expected 1, actual: %v", len(fes))
	}

	bes := fes[0].BackendServices
	if len(bes) != 2 {
		t.Fatalf("Incorrect number of backends, expected 2, actual: %v", len(bes))
	}

	for _, be := range bes {
		eps := be.Endpoints
		if be.Host == "foo.com" {
			if len(eps) != 1 {
				t.Fatalf("Incorrect number of endpoints for foo service, expected 1, actual: %v", len(eps))
			}
		} else if be.Host == "bar.com" {
			if len(eps) != 2 {
				t.Fatalf("Incorrect number of endpoints for bar service, expected 2, actual: %v", len(eps))
			}
		}
	}

	config := configs[0]
	if config.DefaultCert == nil {
		t.Fatal("Default certificate is not set")
	}
	if len(config.Certs) != 1 {
		t.Fatalf("Incorrect number of certs, expected 1, actual: %v", len(config.Certs))
	}
}

func TestTwoServicesMerge(t *testing.T) {
	var portRules []metadata.PortRule
	portRule := metadata.PortRule{
		SourcePort: 80,
		Protocol:   "http",
		Selector:   "http=true",
	}
	portRules = append(portRules, portRule)
	lbConfig := metadata.LBConfig{
		PortRules: portRules,
	}

	glbSvc := metadata.Service{
		Kind:      "loadBalancerService",
		LBConfig:  lbConfig,
		Name:      "glb",
		StackName: "glb",
		UUID:      "glb",
	}
	configs, err := glb.GetGLBConfigs(glbSvc)
	if err != nil {
		t.Fatalf("Error getting configs: %s", err)
	}

	if len(configs) != 1 {
		t.Fatalf("Incorrect number of configs, expected 1, actual: %v", len(configs))
	}

	fes := configs[0].FrontendServices
	if len(fes) != 1 {
		t.Fatalf("Incorrect number of frontends, expected 1, actual: %v", len(fes))
	}

	fe := fes[0]

	bes := fe.BackendServices
	if len(bes) != 1 {
		t.Fatalf("Incorrect number of backends, expected 1, actual: %v", len(bes))
	}
	be := bes[0]
	if be.Host != "foo.com" {
		t.Fatalf("Incorrect hostname, expected foo.com, actual: %v", be.Host)
	}

	if be.Path != "/foo" {
		t.Fatalf("Incorrect path, expected /foo, actual: %v", be.Path)
	}
	eps := be.Endpoints
	if len(eps) != 2 {
		t.Fatalf("Incorrect number of endpoints, expected 2, actual: %v", len(eps))
	}

	for _, ep := range eps {
		if ep.IP != "10.1.1.1" && ep.IP != "10.1.1.3" {
			t.Fatalf("Incorrect ip, expected either 10.1.1.1/3, actual: %v", ep.IP)
		}
		if ep.IP == "10.1.1.1" {
			if ep.Port != 101 {
				t.Fatalf("Incorrect port for foo's container ip, expected 101, actual: %v", ep.Port)
			}
		} else if ep.IP == "10.1.1.3" {
			if ep.Port != 103 {
				t.Fatalf("Incorrect port for foodup's container ip, expected 103, actual: %v", ep.Port)
			}
		}
	}
}

func TestInactiveLB(t *testing.T) {
	var portRules []metadata.PortRule
	portRule := metadata.PortRule{
		SourcePort: 80,
		Protocol:   "http",
		Selector:   "inactive=true",
	}
	portRules = append(portRules, portRule)
	lbConfig := metadata.LBConfig{
		PortRules: portRules,
	}

	glbSvc := metadata.Service{
		Kind:      "loadBalancerService",
		LBConfig:  lbConfig,
		Name:      "glb",
		StackName: "glb",
		UUID:      "glb",
	}
	configs, err := glb.GetGLBConfigs(glbSvc)
	if err != nil {
		t.Fatalf("Error getting configs: %s", err)
	}

	if len(configs) != 1 {
		t.Fatalf("Incorrect number of configs, expected 1, actual: %v", len(configs))
	}

	fes := configs[0].FrontendServices
	if len(fes) != 1 {
		t.Fatalf("Incorrect number of frontends, expected 1, actual: %v", len(fes))
	}

	fe := fes[0]

	bes := fe.BackendServices
	if len(bes) != 1 {
		t.Fatalf("Incorrect number of backends, expected 1, actual: %v", len(bes))
	}
}

func (mf tMetaFetcher) GetSelfHostUUID() (string, error) {
	return "", nil
}

func (mf tMetaFetcher) GetContainer(envUUID string, containerName string) (*metadata.Container, error) {
	return nil, nil
}

func (mf tMetaFetcher) GetServices() ([]metadata.Service, error) {
	var svcs []metadata.Service

	foo, err := mf.GetService("", "foo", "foo")
	if err != nil {
		return nil, err
	}
	svcs = append(svcs, *foo)
	foodup, err := mf.GetService("", "foodup", "foo")
	if err != nil {
		return nil, err
	}
	svcs = append(svcs, *foodup)
	bar, err := mf.GetService("", "bar", "bar")
	if err != nil {
		return nil, err
	}
	svcs = append(svcs, *bar)
	lbBar, err := mf.GetService("", "lbbar", "bar")
	if err != nil {
		return nil, err
	}
	svcs = append(svcs, *lbBar)

	lbFoo, err := mf.GetService("", "lbfoo", "foo")
	if err != nil {
		return nil, err
	}
	svcs = append(svcs, *lbFoo)

	lbFooDup, err := mf.GetService("", "lbfoodup", "foo")
	if err != nil {
		return nil, err
	}
	svcs = append(svcs, *lbFooDup)

	lbInactive, err := mf.GetService("", "lbinactive", "foo")
	if err != nil {
		return nil, err
	}
	svcs = append(svcs, *lbInactive)

	lbActive, err := mf.GetService("", "lbactive", "foo")
	if err != nil {
		return nil, err
	}
	svcs = append(svcs, *lbActive)

	return svcs, nil
}

func (mf tMetaFetcher) GetService(envUUID string, svcName string, stackName string) (*metadata.Service, error) {
	var svc *metadata.Service

	if strings.EqualFold(svcName, "foo") {
		foo := metadata.Service{
			Kind:       "service",
			Containers: getContainers("foo"),
			Name:       "foo",
			StackName:  "foo",
		}
		svc = &foo
	} else if strings.EqualFold(svcName, "bar") {
		bar := metadata.Service{
			Kind:       "service",
			Containers: getContainers("bar"),
			Name:       "bar",
			StackName:  "bar",
		}
		svc = &bar
	} else if strings.EqualFold(svcName, "lbbar") {

		port := metadata.PortRule{
			SourcePort: 80,
			Path:       "/bar",
			Hostname:   "bar.com",
			Service:    "bar/bar",
			Protocol:   "http",
			TargetPort: 102,
		}
		var portRules []metadata.PortRule
		portRules = append(portRules, port)
		lbConfig := metadata.LBConfig{
			PortRules: portRules,
		}
		labels := make(map[string]string)
		labels["glbself"] = "true"
		lbbar := metadata.Service{
			Kind:      "loadBalancerService",
			LBConfig:  lbConfig,
			Name:      "lbbar",
			UUID:      "lbbar",
			StackName: "bar",
			Labels:    labels,
		}
		svc = &lbbar
	} else if strings.EqualFold(svcName, "lbfoo") {
		port := metadata.PortRule{
			SourcePort: 80,
			Path:       "/foo",
			Hostname:   "foo.com",
			Service:    "foo/foo",
			Protocol:   "http",
			TargetPort: 101,
		}
		var portRules []metadata.PortRule
		portRules = append(portRules, port)
		lbConfig := metadata.LBConfig{
			PortRules: portRules,
		}
		labels := make(map[string]string)
		labels["http"] = "true"
		labels["glbself"] = "true"
		lbfoo := metadata.Service{
			Kind:      "loadBalancerService",
			LBConfig:  lbConfig,
			Name:      "lbfoo",
			UUID:      "lbfoo",
			StackName: "foo",
			Labels:    labels,
		}
		svc = &lbfoo
	} else if strings.EqualFold(svcName, "glb") {
		self, err := glb.metaFetcher.GetSelfService()
		if err != nil {
			return nil, err
		}
		svc = &self
	} else if strings.EqualFold(svcName, "lbfoodup") {
		port := metadata.PortRule{
			SourcePort: 80,
			Path:       "/foo",
			Hostname:   "foo.com",
			Service:    "foo/foodup",
			Protocol:   "http",
			TargetPort: 103,
		}
		var portRules []metadata.PortRule
		portRules = append(portRules, port)
		lbConfig := metadata.LBConfig{
			PortRules: portRules,
		}
		labels := make(map[string]string)
		labels["http"] = "true"
		lbfoodup := metadata.Service{
			Kind:      "loadBalancerService",
			LBConfig:  lbConfig,
			Name:      "lbfoodup",
			UUID:      "lbfooddup",
			StackName: "foo",
			Labels:    labels,
		}
		svc = &lbfoodup
	} else if strings.EqualFold(svcName, "foodup") {
		foo := metadata.Service{
			Kind:       "service",
			Containers: getContainers("foodup"),
			Name:       "foodup",
			StackName:  "foo",
		}
		svc = &foo
	} else if strings.EqualFold(svcName, "lbinactive") {
		port := metadata.PortRule{
			SourcePort: 80,
			Path:       "/foo1",
			Hostname:   "foo1.com",
			Service:    "foo/foodup",
			Protocol:   "http",
			TargetPort: 103,
		}
		var portRules []metadata.PortRule
		portRules = append(portRules, port)
		lbConfig := metadata.LBConfig{
			PortRules: portRules,
		}
		labels := make(map[string]string)
		labels["inactive"] = "true"
		lbfoodup := metadata.Service{
			Kind:      "loadBalancerService",
			LBConfig:  lbConfig,
			Name:      "lbinactive",
			UUID:      "lbinactive",
			StackName: "foo",
			Labels:    labels,
			State:     "deactivating",
		}
		svc = &lbfoodup
	} else if strings.EqualFold(svcName, "lbactive") {
		port := metadata.PortRule{
			SourcePort: 80,
			Path:       "/foo2",
			Hostname:   "foo2.com",
			Service:    "foo/foodup",
			Protocol:   "http",
			TargetPort: 103,
		}
		var portRules []metadata.PortRule
		portRules = append(portRules, port)
		lbConfig := metadata.LBConfig{
			PortRules: portRules,
		}
		labels := make(map[string]string)
		labels["inactive"] = "true"
		lbfoodup := metadata.Service{
			Kind:      "loadBalancerService",
			LBConfig:  lbConfig,
			Name:      "lbactive",
			UUID:      "lbactive",
			StackName: "foo",
			Labels:    labels,
			State:     "active",
		}
		svc = &lbfoodup
	}

	return svc, nil
}

func getContainers(svcName string) []metadata.Container {
	containers := []metadata.Container{}
	if strings.EqualFold(svcName, "foo") {
		c := metadata.Container{
			PrimaryIp: "10.1.1.1",
			State:     "running",
		}
		containers = append(containers, c)
	} else if strings.EqualFold(svcName, "bar") {
		c1 := metadata.Container{
			PrimaryIp: "10.1.1.2",
			State:     "running",
		}
		c2 := metadata.Container{
			PrimaryIp: "10.1.1.22",
			State:     "running",
		}
		containers = append(containers, c1, c2)
	} else if strings.EqualFold(svcName, "foodup") {
		c := metadata.Container{
			PrimaryIp: "10.1.1.3",
			State:     "running",
		}
		containers = append(containers, c)
	}
	return containers
}

func (mf tMetaFetcher) GetSelfService() (metadata.Service, error) {
	defaultCert := "glbcert"
	portRule := metadata.PortRule{
		SourcePort: 80,
		Protocol:   "http",
		Selector:   "glbself=true",
	}
	var portRules []metadata.PortRule
	portRules = append(portRules, portRule)
	lbConfig := metadata.LBConfig{
		DefaultCert: defaultCert,
		PortRules:   portRules,
	}
	lbfoo := metadata.Service{
		Kind:      "loadBalancerService",
		LBConfig:  lbConfig,
		Name:      "glb",
		UUID:      "glb",
		StackName: "glb",
	}
	return lbfoo, nil
}

func (mf tMetaFetcher) OnChange(intervalSeconds int, do func(string)) {
}

func (cf tCertFetcher) FetchCertificates(lbMeta *rancher.LBMetadata, isDefaultCert bool) ([]*config.Certificate, error) {
	certs := []*config.Certificate{}
	var defaultCert *config.Certificate

	if !isDefaultCert {
		for _, certName := range lbMeta.Certs {
			cert, err := cf.FetchRancherCertificate(certName)
			if err != nil {
				return nil, err
			}
			certs = append(certs, cert)
		}
	} else {
		if lbMeta.DefaultCert != "" {
			var err error
			defaultCert, err = cf.FetchRancherCertificate(lbMeta.DefaultCert)
			if err != nil {
				return nil, err
			}

			if defaultCert != nil {
				certs = append(certs, defaultCert)
			}
		}
	}
	return certs, nil
}

func (cf tCertFetcher) FetchRancherCertificate(certName string) (*config.Certificate, error) {
	if certName == "" {
		return nil, nil
	}
	return &config.Certificate{}, nil
}

func (cf tCertFetcher) UpdateEndpoints(lbSvc *metadata.Service, eps []client.PublicEndpoint) error {
	return nil
}

func (cf tCertFetcher) ReadAllCertificatesFromDir(certDir string) []*config.Certificate {
	return nil
}

func (cf tCertFetcher) ReadDefaultCertificate(defaultCertDir string) *config.Certificate {
	return nil
}

func (cf tCertFetcher) LookForCertUpdates(do func(string)) {
}

func (p *tProvider) ApplyConfig(lbConfig *config.LoadBalancerConfig) error {
	return nil
}
func (p *tProvider) GetName() string {
	return ""
}

func (p *tProvider) DrainEndpoint(ep *config.Endpoint) bool {
	return false
}

func (p *tProvider) IsEndpointUpForDrain(ep *config.Endpoint) bool {
	return false
}

func (p *tProvider) IsEndpointDrained(ep *config.Endpoint) bool {
	return false
}

func (p *tProvider) RemoveEndpointFromDrain(ep *config.Endpoint) {
}

func (p *tProvider) GetPublicEndpoints(configName string) []string {
	return []string{}
}

func (p *tProvider) CleanupConfig(configName string) error {
	return nil
}

func (p *tProvider) Run(syncEndpointsQueue *utils.TaskQueue) {
}

func (p *tProvider) Stop() error {
	return nil
}

func (p *tProvider) IsHealthy() bool {
	return true
}

func (p *tProvider) ProcessCustomConfig(lbConfig *config.LoadBalancerConfig, customConfig string) error {
	return nil
}
