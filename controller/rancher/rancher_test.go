package rancher

import (
	"strings"
	"testing"

	"fmt"
	"github.com/rancher/go-rancher-metadata/metadata"
	"github.com/rancher/go-rancher/v2"
	"github.com/rancher/lb-controller/config"
	utils "github.com/rancher/lb-controller/utils"
)

var lbc *LoadBalancerController

func init() {
	lbc = &LoadBalancerController{
		stopCh:                     make(chan struct{}),
		incrementalBackoff:         0,
		incrementalBackoffInterval: 5,
		MetaFetcher:                tMetaFetcher{},
		CertFetcher:                tCertFetcher{},
		LBProvider:                 &tProvider{},
	}
}

type tProvider struct {
}

type tCertFetcher struct {
}

type tMetaFetcher struct {
}

func TestTCPRuleFields(t *testing.T) {
	portRules := []metadata.PortRule{}
	port := metadata.PortRule{
		Protocol:   "tcp",
		Path:       "/baz",
		Hostname:   "baz.com",
		Service:    "default/baz",
		TargetPort: 44,
		SourcePort: 45,
	}
	portRules = append(portRules, port)
	meta := &LBMetadata{
		PortRules: portRules,
	}

	configs, _ := lbc.BuildConfigFromMetadata("test", "", "", "any", meta)

	be := configs[0].FrontendServices[0].BackendServices[0]
	if be.Host != "" {
		t.Fatalf("Host is not empty for tcp proto %v", be.Host)
	}

	if be.Path != "" {
		t.Fatalf("Path is not empty for tcp proto %v", be.Path)
	}
}

func TestTwoRunningServices(t *testing.T) {
	portRules := []metadata.PortRule{}
	port := metadata.PortRule{
		Protocol:   "tcp",
		Service:    "default/foo",
		TargetPort: 44,
		SourcePort: 45,
	}
	portRules = append(portRules, port)
	port = metadata.PortRule{
		Protocol:   "tcp",
		Service:    "default/baz",
		TargetPort: 44,
		SourcePort: 45,
	}
	portRules = append(portRules, port)
	meta := &LBMetadata{
		PortRules: portRules,
	}

	configs, _ := lbc.BuildConfigFromMetadata("test", "", "", "any", meta)

	eps := configs[0].FrontendServices[0].BackendServices[0].Endpoints
	if len(eps) != 3 {
		t.Fatalf("Invalid endpoints length %v", len(eps))
	}
}

func TestTwoSourcePorts(t *testing.T) {
	portRules := []metadata.PortRule{}
	port := metadata.PortRule{
		Hostname:   "foo.com",
		Protocol:   "http",
		Service:    "default/foo",
		TargetPort: 44,
		SourcePort: 45,
	}
	portRules = append(portRules, port)
	port = metadata.PortRule{
		Hostname:   "baz.com",
		Protocol:   "http",
		Service:    "default/baz",
		TargetPort: 44,
		SourcePort: 46,
	}
	portRules = append(portRules, port)
	meta := &LBMetadata{
		PortRules: portRules,
	}

	configs, _ := lbc.BuildConfigFromMetadata("test", "", "", "any", meta)

	fes := configs[0].FrontendServices
	if len(fes) != 2 {
		t.Fatalf("Invalid frontend length %v", len(fes))
	}
	for _, fe := range fes {
		bes := fe.BackendServices
		if len(bes) != 1 {
			t.Fatalf("Invalid backend length %v", len(bes))
		}
	}
}

func TestOneSourcePortTwoRules(t *testing.T) {
	portRules := []metadata.PortRule{}
	port := metadata.PortRule{
		Hostname:   "foo.com",
		Protocol:   "http",
		Service:    "default/foo",
		TargetPort: 44,
		SourcePort: 45,
	}
	portRules = append(portRules, port)
	port = metadata.PortRule{
		Hostname:   "baz.com",
		Protocol:   "http",
		Service:    "default/baz",
		TargetPort: 44,
		SourcePort: 45,
	}
	portRules = append(portRules, port)
	meta := &LBMetadata{
		PortRules: portRules,
	}

	configs, _ := lbc.BuildConfigFromMetadata("test", "", "", "any", meta)

	fes := configs[0].FrontendServices
	if len(fes) != 1 {
		t.Fatalf("Invalid frontend length %v", len(fes))
	}
	for _, fe := range fes {
		bes := fe.BackendServices
		if len(bes) != 2 {
			t.Fatalf("Invalid backend length %v", len(bes))
		}
	}
}

func TestPreferLocalService(t *testing.T) {
	portRules := []metadata.PortRule{}
	port := metadata.PortRule{
		Protocol:   "tcp",
		Service:    "default/local",
		TargetPort: 44,
		SourcePort: 45,
	}
	portRules = append(portRules, port)
	meta := &LBMetadata{
		PortRules: portRules,
	}

	configs, _ := lbc.BuildConfigFromMetadata("test", "", "3", "prefer-local", meta)

	eps := configs[0].FrontendServices[0].BackendServices[0].Endpoints
	if len(eps) != 2 {
		t.Fatalf("Invalid endpoints length %v", len(eps))
	}

}

func TestLocalService(t *testing.T) {
	portRules := []metadata.PortRule{}
	port := metadata.PortRule{
		Protocol:   "tcp",
		Service:    "default/local",
		TargetPort: 44,
		SourcePort: 45,
	}
	portRules = append(portRules, port)
	meta := &LBMetadata{
		PortRules: portRules,
	}

	configs, _ := lbc.BuildConfigFromMetadata("test", "", "1", "only-local", meta)

	eps := configs[0].FrontendServices[0].BackendServices[0].Endpoints
	if len(eps) != 1 {
		t.Fatalf("Invalid endpoints length %v", len(eps))
	}

}

func TestStoppedAndRunningInstance(t *testing.T) {
	portRules := []metadata.PortRule{}
	port := metadata.PortRule{
		Protocol:   "tcp",
		Service:    "default/foo",
		TargetPort: 44,
		SourcePort: 45,
	}
	portRules = append(portRules, port)
	port = metadata.PortRule{
		Protocol:   "tcp",
		Service:    "default/bar",
		TargetPort: 44,
		SourcePort: 45,
	}
	portRules = append(portRules, port)
	meta := &LBMetadata{
		PortRules: portRules,
	}

	configs, _ := lbc.BuildConfigFromMetadata("test", "", "", "any", meta)

	eps := configs[0].FrontendServices[0].BackendServices[0].Endpoints
	if len(eps) != 1 {
		t.Fatalf("Invalid endpoints length %v", len(eps))
	}
}

func TestStoppedInstance(t *testing.T) {
	portRules := []metadata.PortRule{}
	port := metadata.PortRule{
		Protocol:   "tcp",
		Service:    "default/bar",
		TargetPort: 44,
		SourcePort: 45,
	}
	portRules = append(portRules, port)
	meta := &LBMetadata{
		PortRules: portRules,
	}

	configs, _ := lbc.BuildConfigFromMetadata("test", "", "", "any", meta)

	eps := configs[0].FrontendServices[0].BackendServices[0].Endpoints
	if len(eps) != 0 {
		t.Fatalf("Invalid endpoints length %v", len(eps))
	}
}

func TestInactiveService(t *testing.T) {
	portRules := []metadata.PortRule{}
	port1 := metadata.PortRule{
		Protocol:   "tcp",
		Service:    "default/inactive",
		TargetPort: 44,
		SourcePort: 45,
	}
	port2 := metadata.PortRule{
		Protocol:   "tcp",
		Service:    "default/foo",
		TargetPort: 46,
		SourcePort: 45,
	}
	portRules = append(portRules, port1)
	portRules = append(portRules, port2)
	meta := &LBMetadata{
		PortRules: portRules,
	}

	configs, _ := lbc.BuildConfigFromMetadata("test", "", "", "any", meta)

	fe := configs[0].FrontendServices[0]
	bes := fe.BackendServices
	if len(bes) != 1 {
		t.Fatalf("Invalid backends length, expected 1 for 1 active and 1 inactive service: [%v]", len(bes))
	}
}

func TestPriority(t *testing.T) {
	portRules := []metadata.PortRule{}
	port0 := metadata.PortRule{
		Protocol:   "http",
		Service:    "default/priority",
		Hostname:   "fooff",
		TargetPort: 44,
		SourcePort: 45,
		Priority:   3,
	}
	port1 := metadata.PortRule{
		Protocol:   "http",
		Service:    "default/priority",
		Hostname:   "foo",
		TargetPort: 44,
		SourcePort: 45,
		Priority:   100,
	}
	port2 := metadata.PortRule{
		Protocol:   "http",
		Service:    "default/priority",
		Hostname:   "*.bar",
		TargetPort: 44,
		SourcePort: 45,
		Priority:   2,
	}
	port3 := metadata.PortRule{
		Protocol:   "http",
		Service:    "default/priority",
		Hostname:   "baz",
		TargetPort: 44,
		SourcePort: 45,
		Priority:   4,
	}
	port4 := metadata.PortRule{
		Protocol:   "http",
		Service:    "default/priority",
		Hostname:   "bazfsd",
		TargetPort: 44,
		SourcePort: 45,
		Priority:   1,
	}

	portRules = append(portRules, port0)
	portRules = append(portRules, port1)
	portRules = append(portRules, port2)
	portRules = append(portRules, port3)
	portRules = append(portRules, port4)

	meta := &LBMetadata{
		PortRules: portRules,
	}

	configs, _ := lbc.BuildConfigFromMetadata("test", "", "", "any", meta)

	bes := configs[0].FrontendServices[0].BackendServices
	if len(bes) != 5 {
		t.Fatalf("Invalid backend length [%v]", len(bes))
	}

	if bes[0].Host != port4.Hostname {
		t.Fatalf("Invalid order for the 1st element: [%v]", bes[0].UUID)
	}
	if bes[1].Host != ".bar" {
		t.Fatalf("Invalid order for the 2nd element: [%v]", bes[1].UUID)
	}
	if bes[2].Host != port0.Hostname {
		t.Fatalf("Invalid order for the 3rd element: [%v]", bes[2].Host)
	}
	if bes[3].Host != port3.Hostname {
		t.Fatalf("Invalid order for the 4th element: [%v]", bes[3].Host)
	}
	if bes[4].Host != port1.Hostname {
		t.Fatalf("Invalid order for the 5th element: [%v]", bes[4].Host)
	}
}

func TestPriorityExtra(t *testing.T) {
	portRules := []metadata.PortRule{}
	port0 := metadata.PortRule{
		Protocol:   "http",
		Service:    "default/priority",
		Hostname:   "*.fooff",
		TargetPort: 44,
		SourcePort: 45,
		Priority:   1,
	}
	port1 := metadata.PortRule{
		Protocol:   "http",
		Service:    "default/priority",
		Hostname:   "foo",
		TargetPort: 44,
		SourcePort: 45,
		Priority:   2,
	}

	portRules = append(portRules, port0)
	portRules = append(portRules, port1)

	meta := &LBMetadata{
		PortRules: portRules,
	}

	configs, _ := lbc.BuildConfigFromMetadata("test", "", "", "any", meta)

	bes := configs[0].FrontendServices[0].BackendServices
	if len(bes) != 2 {
		t.Fatalf("Invalid backend length [%v]", len(bes))
	}

	if bes[0].Host != ".fooff" {
		t.Fatalf("Invalid order for the 1st element: [%v]", bes[0].UUID)
	}

	if bes[1].Host != port1.Hostname {
		t.Fatalf("Invalid order for the 1st element: [%v]", bes[1].UUID)
	}

	//swap order
	portRules = []metadata.PortRule{}
	portRules = append(portRules, port1)
	portRules = append(portRules, port0)
	meta = &LBMetadata{
		PortRules: portRules,
	}

	configs, _ = lbc.BuildConfigFromMetadata("test", "", "", "any", meta)
	bes = configs[0].FrontendServices[0].BackendServices
	if len(bes) != 2 {
		t.Fatalf("Invalid backend length [%v]", len(bes))
	}

	if bes[0].Host != ".fooff" {
		t.Fatalf("Invalid order for the 1st element: [%v]", bes[0].UUID)
	}

	if bes[1].Host != port1.Hostname {
		t.Fatalf("Invalid order for the 1st element: [%v]", bes[1].UUID)
	}
}

func TestRuleFields(t *testing.T) {
	portRules := []metadata.PortRule{}
	port := metadata.PortRule{
		SourcePort:  12,
		Protocol:    "http",
		Path:        "/baz",
		Hostname:    "baz.com",
		Service:     "default/baz",
		TargetPort:  13,
		BackendName: "mybackend",
	}
	portRules = append(portRules, port)
	meta := &LBMetadata{
		PortRules: portRules,
	}

	configs, err := lbc.BuildConfigFromMetadata("test", "", "", "any", meta)
	if err != nil {
		t.Fatalf("Failed to build the config from metadata %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("Invalid config length %v", len(configs))
	}
	config := configs[0]
	if config.Name != "test" {
		t.Fatalf("Invalid config name %s. Expected \"test\"", config.Name)
	}
	if len(config.FrontendServices) < 1 {
		t.Fatalf("Invalid frontend length %v", len(config.FrontendServices))
	}
	fe := config.FrontendServices[0]
	if fe.Name != "12" {
		t.Fatalf("Invalid frontend name %v", fe.Name)
	}
	if fe.Port != 12 {
		t.Fatalf("Invalid frontend port %v", fe.Port)
	}

	if fe.Protocol != "http" {
		t.Fatalf("Invalid frontend proto %v", fe.Protocol)
	}

	if len(fe.BackendServices) != 1 {
		t.Fatalf("Invalid backend length %v", len(fe.BackendServices))
	}
	be := fe.BackendServices[0]
	if len(be.Endpoints) != 2 {
		t.Fatalf("Invalid endpoints length %v", len(be.Endpoints))
	}

	if be.UUID != "mybackend" {
		t.Fatalf("Invalid backend name %v", be.UUID)
	}

	if be.Path != "/baz" {
		t.Fatalf("Invalid path %v", be.Path)
	}

	if be.Host != "baz.com" {
		t.Fatalf("Invalid hostname %v", be.Host)
	}

	if be.Protocol != "http" {
		t.Fatalf("Invalid protocol %v", be.Host)
	}

	for _, ep := range be.Endpoints {
		ip := ep.IP
		if !(ip == "10.1.1.3" || ip == "10.1.1.4") {
			t.Fatalf("Invalid ip %v", ip)
		}

		if !(ep.Name == "47ccfd4e625996c751ea49c3ca9d4c68f39fe477" || ep.Name == "50c65454e2804da4e25294f7a4a3efe332acde0e") {
			t.Fatalf("Invalid ep name %v", ep.Name)
		}
		if ep.Port != 13 {
			t.Fatalf("Invalid ep port %v", ep.Port)
		}
	}
}

func (mf tMetaFetcher) GetServices() ([]metadata.Service, error) {
	var svcs []metadata.Service
	port := metadata.PortRule{
		Path:        "/baz",
		Hostname:    "baz.com",
		TargetPort:  46,
		BackendName: "baz",
		//Service:     "default/baz",
	}

	var portRules []metadata.PortRule
	portRules = append(portRules, port)

	lbConfig := metadata.LBConfig{
		PortRules: portRules,
	}

	labels := make(map[string]string)
	labels["foo"] = "bar"
	svc := metadata.Service{
		Kind:       "service",
		Containers: getContainers("selector"),
		Labels:     labels,
		LBConfig:   lbConfig,
		Name:       "baz",
		StackName:  "default",
	}

	labels = make(map[string]string)
	labels["a"] = "b"
	svc1 := metadata.Service{
		Kind:       "service",
		Containers: getContainers("ab"),
		Labels:     labels,
		Name:       "a",
		StackName:  "b",
	}

	svcs = append(svcs, svc, svc1)
	return svcs, nil
}

func (mf tMetaFetcher) GetContainer(envUUID string, containerName string) (*metadata.Container, error) {
	return nil, nil
}

func (mf tMetaFetcher) GetRegionName() (string, error) {
	return "", nil
}

func (mf tMetaFetcher) GetService(link string) (*metadata.Service, error) {
	var svc *metadata.Service
	splitSvcName := strings.Split(link, "/")
	svcName := splitSvcName[1]
	if strings.EqualFold(svcName, "foo") {
		svc = &metadata.Service{
			Kind:       "service",
			Containers: getContainers(svcName),
		}
	} else if strings.EqualFold(svcName, "bar") {
		svc = &metadata.Service{
			Kind:       "service",
			Containers: getContainers(svcName),
		}
	} else if strings.EqualFold(svcName, "baz") {
		svc = &metadata.Service{
			Kind:       "service",
			Containers: getContainers(svcName),
		}
	} else if strings.EqualFold(svcName, "alias") {
		svc = &metadata.Service{
			Kind:  "dnsService",
			Links: map[string]string{"default/foo": "", "default/bar": ""},
		}
	} else if strings.EqualFold(svcName, "ext") {
		ips := []string{"172.0.0.10"}
		svc = &metadata.Service{
			Kind:        "externalService",
			ExternalIps: ips,
		}
	} else if strings.EqualFold(svcName, "selector") {
		svc = &metadata.Service{
			Kind:       "service",
			Containers: getContainers("selector"),
		}
	} else if strings.EqualFold(svcName, "priority") {
		svc = &metadata.Service{
			Kind:       "service",
			Containers: getContainers("priority"),
		}
	} else if strings.EqualFold(svcName, "a") {
		svc = &metadata.Service{
			Kind:       "service",
			Containers: getContainers("ab"),
			Name:       "a",
			StackName:  "b",
		}
	} else if strings.EqualFold(svcName, "extcname") {
		svc = &metadata.Service{
			Kind:     "externalService",
			Hostname: "google.com",
		}
	} else if strings.EqualFold(svcName, "inactive") {
		svc = &metadata.Service{
			Kind:       "service",
			State:      "inactive",
			Containers: getContainers("inactive"),
		}
	} else if strings.EqualFold(svcName, "local") {
		svc = &metadata.Service{
			Kind:       "service",
			Containers: getContainers(svcName),
		}
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
		c := metadata.Container{
			PrimaryIp: "10.1.1.2",
			State:     "stopped",
		}
		containers = append(containers, c)
	} else if strings.EqualFold(svcName, "baz") {
		c1 := metadata.Container{
			PrimaryIp: "10.1.1.3",
			State:     "running",
		}
		c2 := metadata.Container{
			PrimaryIp: "10.1.1.4",
			State:     "starting",
		}
		containers = append(containers, c1)
		containers = append(containers, c2)
	} else if strings.EqualFold(svcName, "selector") {
		c1 := metadata.Container{
			PrimaryIp: "10.1.1.10",
			State:     "running",
		}
		containers = append(containers, c1)
	} else if strings.EqualFold(svcName, "priority") {
		c1 := metadata.Container{
			PrimaryIp: "10.1.1.10",
			State:     "running",
		}
		containers = append(containers, c1)
	} else if strings.EqualFold(svcName, "ab") {
		c1 := metadata.Container{
			PrimaryIp: "10.1.1.11",
			State:     "running",
		}
		containers = append(containers, c1)
	} else if strings.EqualFold(svcName, "inactive") {
		c1 := metadata.Container{
			PrimaryIp: "10.1.1.12",
			State:     "running",
		}
		containers = append(containers, c1)
	} else if strings.EqualFold(svcName, "local") {
		cx := []metadata.Container{
			{
				PrimaryIp: "10.1.1.13",
				State:     "running",
				HostUUID:  "1",
			},
			{
				PrimaryIp: "10.1.1.14",
				State:     "running",
				HostUUID:  "2",
			},
		}
		containers = append(containers, cx...)
	}
	return containers
}

func TestExternalCname(t *testing.T) {
	portRules := []metadata.PortRule{}
	port := metadata.PortRule{
		Protocol:   "tcp",
		Service:    "default/extcname",
		TargetPort: 44,
		SourcePort: 45,
	}
	portRules = append(portRules, port)
	meta := &LBMetadata{
		PortRules: portRules,
	}

	configs, _ := lbc.BuildConfigFromMetadata("test", "", "", "any", meta)

	eps := configs[0].FrontendServices[0].BackendServices[0].Endpoints
	if len(eps) != 1 {
		t.Fatalf("Invalid endpoints length %v", len(eps))
	}

	if eps[0].IP != "google.com" {
		t.Fatalf("Invalid endpoint target, expected [google.com], actual %s", eps[0].IP)
	}

	if !eps[0].IsCname {
		t.Fatal("IsCname should have been set to true, but actual value is false")
	}
}

func (mf tMetaFetcher) GetSelfService() (metadata.Service, error) {
	var svc metadata.Service
	return svc, nil
}

func (mf tMetaFetcher) GetSelfHostUUID() (string, error) {
	return "", nil
}

func (mf tMetaFetcher) OnChange(intervalSeconds int, do func(string)) {
}

func (mf tMetaFetcher) GetServicesFromRegionEnvironment(regionName string, envName string) ([]metadata.Service, error) {
	var svcs []metadata.Service
	return svcs, nil
}

func (mf tMetaFetcher) GetServicesInLocalRegion(envName string) ([]metadata.Service, error) {
	var svcs []metadata.Service
	return svcs, nil
}

func (mf tMetaFetcher) GetServiceFromRegionEnvironment(regionName string, envName string, stackName string, svcName string) (metadata.Service, error) {
	var svc metadata.Service
	return svc, nil
}

func (mf tMetaFetcher) GetServiceInLocalRegion(envName string, stackName string, svcName string) (metadata.Service, error) {
	var svc metadata.Service
	return svc, nil
}

func (mf tMetaFetcher) GetServiceInLocalEnvironment(stackName string, svcName string) (metadata.Service, error) {
	var svc metadata.Service
	return svc, nil
}

func (cf tCertFetcher) FetchCertificates(lbMeta *LBMetadata, isDefaultCert bool) ([]*config.Certificate, error) {
	return nil, nil
}

func (cf tCertFetcher) FetchCertificate(certName string) (*config.Certificate, error) {
	return nil, nil
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

func (p *tProvider) GetPublicEndpoints(configName string) ([]string, error) {
	return []string{}, nil
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

func (p *tProvider) GetExistingConfigNames() (map[string]bool, error) {
	return nil, fmt.Errorf("method is not implemented")
}

func TestSelectorNoMatch(t *testing.T) {
	portRules := []metadata.PortRule{}
	port := metadata.PortRule{
		Protocol:   "http",
		SourcePort: 45,
		Selector:   "foo1=bar1",
	}
	portRules = append(portRules, port)
	meta := &LBMetadata{
		PortRules: portRules,
	}

	lbc.processSelector(meta)

	configs, _ := lbc.BuildConfigFromMetadata("test", "", "", "any", meta)

	if len(configs[0].FrontendServices) != 0 {
		t.Fatalf("Incorrect number of frontend services %v", len(configs[0].FrontendServices))
	}
}

func TestSelectorMatch(t *testing.T) {
	portRules := []metadata.PortRule{}
	port := metadata.PortRule{
		Protocol:   "http",
		SourcePort: 45,
		Selector:   "foo=bar",
	}
	portRules = append(portRules, port)
	meta := &LBMetadata{
		PortRules: portRules,
	}

	lbc.processSelector(meta)

	configs, _ := lbc.BuildConfigFromMetadata("test", "", "", "any", meta)

	fe := configs[0].FrontendServices[0]
	if len(fe.BackendServices) == 0 {
		t.Fatal("No backends are configured for selector based service")
	}

	be := fe.BackendServices[0]

	if fe.Port != 45 {
		t.Fatalf("Port is incorrect %v", fe.Port)
	}

	if fe.Protocol != "http" {
		t.Fatalf("Proto is incorrect %v", fe.Protocol)
	}

	if be.Host != "baz.com" {
		t.Fatalf("Host is incorrect %v", be.Host)
	}

	if be.Path != "/baz" {
		t.Fatalf("Path is incorrect %v", be.Path)
	}

	if be.Port != 46 {
		t.Fatalf("Port is incorrect %v", be.Port)
	}

	if be.UUID != "baz" {
		t.Fatalf("Backend name is incorrect %v", be.UUID)
	}
}

func TestSelectorMatchNoTargetPort(t *testing.T) {
	portRules := []metadata.PortRule{}
	port := metadata.PortRule{
		Protocol:   "http",
		SourcePort: 45,
		Selector:   "a=b",
		TargetPort: 46,
		Hostname:   "ab.com",
		Path:       "/ab",
	}
	portRules = append(portRules, port)
	meta := &LBMetadata{
		PortRules: portRules,
	}

	lbc.processSelector(meta)

	configs, _ := lbc.BuildConfigFromMetadata("test", "", "", "any", meta)

	if len(configs[0].FrontendServices) == 0 {
		t.Fatal("No frontends are configured for selector based service")
	}

	fe := configs[0].FrontendServices[0]
	if len(fe.BackendServices) == 0 {
		t.Fatal("No backends are configured for selector based service")
	}

	be := fe.BackendServices[0]

	if fe.Port != 45 {
		t.Fatalf("Port is incorrect %v", fe.Port)
	}

	if fe.Protocol != "http" {
		t.Fatalf("Proto is incorrect %v", fe.Protocol)
	}

	if be.Host != "ab.com" {
		t.Fatalf("Host is incorrect %v", be.Host)
	}

	if be.Path != "/ab" {
		t.Fatalf("Path is incorrect %v", be.Path)
	}

	if be.Port != 46 {
		t.Fatalf("Port is incorrect %v", be.Port)
	}
}
