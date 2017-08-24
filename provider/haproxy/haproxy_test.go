package haproxy

import (
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/patrickmn/go-cache"
	"github.com/rancher/lb-controller/config"
	"io/ioutil"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

var lbp Provider

func init() {
	haproxyCfg := &haproxyConfig{
		ReloadCmd: "haproxy_reload /etc/haproxy/haproxy.cfg reload",
		StartCmd:  "haproxy_reload /etc/haproxy/haproxy.cfg start",
		Config:    "test_data/haproxy_new.cfg",
		Template:  "test_data/haproxy_template.cfg",
		CertDir:   "/etc/haproxy/certs",
	}
	dm := &drainMgr{
		drainList:   make(map[string]int),
		mu:          &sync.RWMutex{},
		haproxySock: "/var/run/haproxy.sock",
		pollResults: cache.New(5*time.Minute, 10*time.Minute),
	}
	dm.drainList["drainedS1"] = 0
	lbp = Provider{
		cfg:      haproxyCfg,
		stopCh:   make(chan struct{}),
		init:     true,
		drainMgr: dm,
	}
}

func TestBuildCustomConfigDefault(t *testing.T) {
	customConfig, err := getCustomConfig("custom_config_default")
	if err != nil {
		t.Fatalf("Failed to read custom config: %v", err)
	}
	lbConfig := &config.LoadBalancerConfig{}
	err = lbp.ProcessCustomConfig(lbConfig, customConfig)
	if err != nil {
		t.Fatalf("Error while process custom config: %v", err)
	}

	result, err := validateCustomConfig("custom_config_default_resp", lbConfig.Config)
	if err != nil {
		t.Fatalf("Error validating custom config: %v", err)
	}

	if !result {
		t.Fatal("Configs don't match")
	}
}

func TestBuildCustomConfigAppend(t *testing.T) {
	customConfig, err := getCustomConfig("custom_config_append")
	if err != nil {
		t.Fatalf("Failed to read custom config: %v", err)
	}
	lbConfig := &config.LoadBalancerConfig{}
	err = lbp.ProcessCustomConfig(lbConfig, customConfig)
	if err != nil {
		t.Fatalf("Error while process custom config: %v", err)
	}

	result, err := validateCustomConfig("custom_config_append_resp", lbConfig.Config)
	if err != nil {
		t.Fatalf("Error validating custom config: %v", err)
	}

	if !result {
		t.Fatal("Configs don't match")
	}
}

func TestBuildCustomConfigOverride(t *testing.T) {
	customConfig, err := getCustomConfig("custom_config_override")
	if err != nil {
		t.Fatalf("Failed to read custom config: %v", err)
	}
	lbConfig := &config.LoadBalancerConfig{}
	err = lbp.ProcessCustomConfig(lbConfig, customConfig)
	if err != nil {
		t.Fatalf("Error while process custom config: %v", err)
	}

	result, err := validateCustomConfig("custom_config_override_resp", lbConfig.Config)
	if err != nil {
		t.Fatalf("Error validating custom config: %v", err)
	}

	if !result {
		t.Fatal("Configs don't match")
	}
}

func TestBuildCustomConfigExtraSection(t *testing.T) {
	customConfig, err := getCustomConfig("custom_config_extra")
	if err != nil {
		t.Fatalf("Failed to read custom config: %v", err)
	}
	lbConfig := &config.LoadBalancerConfig{}
	err = lbp.ProcessCustomConfig(lbConfig, customConfig)
	if err != nil {
		t.Fatalf("Error while process custom config: %v", err)
	}

	result, err := validateCustomConfig("custom_config_extra_resp", lbConfig.Config)
	if err != nil {
		t.Fatalf("Error validating custom config: %v", err)
	}

	if !result {
		t.Fatal("Configs don't match")
	}
}

func TestBuildCustomConfigExtraFrontend(t *testing.T) {
	customConfig, err := getCustomConfig("custom_config_extra_frontend")
	if err != nil {
		t.Fatalf("Failed to read custom config: %v", err)
	}
	backends := []*config.BackendService{}
	var eps config.Endpoints
	ep := &config.Endpoint{
		Name: "s1",
		IP:   "10.1.1.1",
		Port: 90,
	}
	eps = append(eps, ep)
	backend := &config.BackendService{
		UUID:      "bar",
		Port:      8080,
		Protocol:  config.HTTPProto,
		Endpoints: eps,
	}
	backends = append(backends, backend)
	frontend := &config.FrontendService{
		Name:            "foo",
		Port:            80,
		Protocol:        config.HTTPProto,
		BackendServices: backends,
	}

	frontends := []*config.FrontendService{}
	frontends = append(frontends, frontend)

	lbConfig := &config.LoadBalancerConfig{
		FrontendServices: frontends,
	}
	err = lbp.ProcessCustomConfig(lbConfig, customConfig)
	if err != nil {
		t.Fatalf("Error while process custom config: %v", err)
	}

	result, err := validateCustomConfig("custom_config_extra_frontend_resp", lbConfig.Config)
	if err != nil {
		t.Fatalf("Error validating custom config: %v", err)
	}

	if !result {
		t.Fatal("Configs don't match")
	}

	result, err = validateCustomConfig("custom_config_extra_frontend_f_resp", lbConfig.FrontendServices[0].Config)
	if err != nil {
		t.Fatalf("Error validating custom config: %v", err)
	}

	if !result {
		t.Fatal("Configs don't match")
	}
}

func TestBuildCustomConfigBackendSection(t *testing.T) {
	customConfig, err := getCustomConfig("custom_config_frontend_backend")
	if err != nil {
		t.Fatalf("Failed to read custom config: %v", err)
	}
	backends := []*config.BackendService{}
	var eps config.Endpoints
	ep := &config.Endpoint{
		Name: "s1",
		IP:   "10.1.1.1",
		Port: 90,
	}
	eps = append(eps, ep)
	backend := &config.BackendService{
		UUID:      "bar",
		Port:      8080,
		Protocol:  config.HTTPProto,
		Endpoints: eps,
	}
	backends = append(backends, backend)
	frontend := &config.FrontendService{
		Name:            "foo",
		Port:            80,
		Protocol:        config.HTTPProto,
		BackendServices: backends,
	}

	frontends := []*config.FrontendService{}
	frontends = append(frontends, frontend)

	lbConfig := &config.LoadBalancerConfig{
		FrontendServices: frontends,
	}
	err = lbp.ProcessCustomConfig(lbConfig, customConfig)
	if err != nil {
		t.Fatalf("Error while process custom config: %v", err)
	}

	result, err := validateCustomConfig("custom_config_frontend_resp", lbConfig.FrontendServices[0].Config)
	if err != nil {
		t.Fatalf("Error validating custom config: %v", err)
	}

	if !result {
		t.Fatal("Configs don't match")
	}

	result, err = validateCustomConfig("custom_config_backend_resp", lbConfig.FrontendServices[0].BackendServices[0].Config)
	if err != nil {
		t.Fatalf("Error validating custom config: %v", err)
	}

	if !result {
		t.Fatal("Configs don't match")
	}
}

func TestBuildCustomConfigBackendServer(t *testing.T) {
	customConfig, err := getCustomConfig("custom_config_backend_server")
	if err != nil {
		t.Fatalf("Failed to read custom config: %v", err)
	}
	backends := []*config.BackendService{}
	var eps1 config.Endpoints
	ep1 := &config.Endpoint{
		Name: "s1",
		IP:   "10.1.1.1",
		Port: 90,
	}
	eps1 = append(eps1, ep1)
	backend := &config.BackendService{
		UUID:      "foo",
		Port:      8080,
		Protocol:  config.HTTPProto,
		Endpoints: eps1,
	}
	var eps2 config.Endpoints
	ep2 := &config.Endpoint{
		Name: "s2",
		IP:   "10.1.1.2",
		Port: 90,
	}
	eps2 = append(eps2, ep2)
	backends = append(backends, backend)
	backend = &config.BackendService{
		UUID:      "foofoo",
		Port:      8080,
		Protocol:  config.HTTPProto,
		Endpoints: eps2,
	}
	backends = append(backends, backend)

	frontends := []*config.FrontendService{}
	frontend := &config.FrontendService{
		Name:            "foo",
		Port:            80,
		Protocol:        config.HTTPProto,
		BackendServices: backends,
	}
	frontends = append(frontends, frontend)

	lbConfig := &config.LoadBalancerConfig{
		FrontendServices: frontends,
	}
	err = lbp.ProcessCustomConfig(lbConfig, customConfig)
	if err != nil {
		t.Fatalf("Error while process custom config: %v", err)
	}

	result, err := validateCustomConfig("custom_config_backend1_resp", lbConfig.FrontendServices[0].BackendServices[0].Config)
	if err != nil {
		t.Fatalf("Error validating custom config: %v", err)
	}

	if !result {
		t.Fatal("Configs don't match")
	}

	result, err = validateCustomConfig("custom_config_backend2_resp", lbConfig.FrontendServices[0].BackendServices[1].Config)
	if err != nil {
		t.Fatalf("Error validating custom config: %v", err)
	}

	if !result {
		t.Fatal("Configs don't match")
	}

	result, err = validateCustomConfig("", lbConfig.FrontendServices[0].BackendServices[1].Endpoints[0].Config)
	if err != nil {
		t.Fatalf("Error validating custom config: %v", err)
	}

	if !result {
		t.Fatal("Configs don't match")
	}
	result, err = validateCustomConfig("custom_config_server_resp", lbConfig.FrontendServices[0].BackendServices[0].Endpoints[0].Config)
	if err != nil {
		t.Fatalf("Error validating custom config: %v", err)
	}

	if !result {
		t.Fatal("Configs don't match")
	}
}

func getCustomConfig(name string) (string, error) {
	b, err := ioutil.ReadFile(fmt.Sprintf("test_data/%s", name))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func validateCustomConfig(name string, customConfig string) (bool, error) {
	resp := ""
	if name != "" {
		b, err := ioutil.ReadFile(fmt.Sprintf("test_data/%s", name))
		if err != nil {
			return false, err
		}
		resp = string(b)
	}

	result := customConfig == resp
	if !result {
		err := ioutil.WriteFile("/tmp/dat1", []byte(customConfig), 0644)
		logrus.Infof("Error is:\n%v", err)
		logrus.Infof("Expected result:\n%s", resp)
		logrus.Infof("Actual result:\n%s", customConfig)
	}
	return result, nil
}

func TestBuildCustomConfigFrontendBind(t *testing.T) {
	customConfig, err := getCustomConfig("custom_config_frontend_bind")
	if err != nil {
		t.Fatalf("Failed to read custom config: %v", err)
	}
	backends := []*config.BackendService{}
	var eps config.Endpoints
	ep := &config.Endpoint{
		Name: "s1",
		IP:   "10.1.1.1",
		Port: 90,
	}
	eps = append(eps, ep)
	backend := &config.BackendService{
		UUID:      "bar",
		Port:      8080,
		Protocol:  config.HTTPProto,
		Endpoints: eps,
	}
	backends = append(backends, backend)
	frontend := &config.FrontendService{
		Name:            "foo",
		Port:            80,
		Protocol:        config.HTTPProto,
		BackendServices: backends,
	}

	frontends := []*config.FrontendService{}
	frontends = append(frontends, frontend)

	lbConfig := &config.LoadBalancerConfig{
		FrontendServices: frontends,
	}
	err = lbp.ProcessCustomConfig(lbConfig, customConfig)
	if err != nil {
		t.Fatalf("Error while process custom config: %v", err)
	}

	result, err := validateCustomConfig("custom_config_frontend_bind_resp", lbConfig.FrontendServices[0].Config)
	if err != nil {
		t.Fatalf("Error validating custom config: %v", err)
	}

	if !result {
		t.Fatal("Configs don't match")
	}

	if !lbConfig.FrontendServices[0].AcceptProxy {
		t.Fatal("Accept proxy is not set on the config")
	}
}

func TestBuildCustomConfigUsers(t *testing.T) {
	customConfig, err := getCustomConfig("custom_config_users")
	if err != nil {
		t.Fatalf("Failed to read custom config: %v", err)
	}
	lbConfig := &config.LoadBalancerConfig{}
	err = lbp.ProcessCustomConfig(lbConfig, customConfig)
	if err != nil {
		t.Fatalf("Error while process custom config: %v", err)
	}

	result, err := validateCustomConfig("custom_config_users_resp", lbConfig.Config)
	if err != nil {
		t.Fatalf("Error validating custom config: %v", err)
	}

	if !result {
		t.Fatal("Configs don't match")
	}
}

func TestCnameEndpointServer(t *testing.T) {
	backends := []*config.BackendService{}
	var eps config.Endpoints
	ep := &config.Endpoint{
		Name:    "s1",
		IP:      "google.com",
		Port:    90,
		IsCname: true,
	}
	eps = append(eps, ep)
	backend := &config.BackendService{
		UUID:      "google.com",
		Port:      8080,
		Protocol:  config.HTTPProto,
		Endpoints: eps,
	}
	backends = append(backends, backend)

	frontends := []*config.FrontendService{}
	frontend := &config.FrontendService{
		Name:            "google.com",
		Port:            80,
		Protocol:        config.HTTPProto,
		BackendServices: backends,
	}
	frontends = append(frontends, frontend)

	lbConfig := &config.LoadBalancerConfig{
		FrontendServices: frontends,
	}
	err := lbp.ProcessCustomConfig(lbConfig, "")
	if err != nil {
		t.Fatalf("Error while process custom config: %v", err)
	}

	expected := "  check resolvers rancher"
	actual := lbConfig.FrontendServices[0].BackendServices[0].Endpoints[0].Config
	result := strings.EqualFold(expected, actual)

	if !result {
		t.Fatalf("Configs don't match; expected [%s], actual [%s]", expected, actual)
	}

}

func TestHaproxyConfigWriteDefaultCert(t *testing.T) {
	backends := []*config.BackendService{}
	var eps config.Endpoints
	ep := &config.Endpoint{
		Name: "s1",
		IP:   "10.1.1.1",
		Port: 90,
	}
	eps = append(eps, ep)
	backend := &config.BackendService{
		UUID:      "bar",
		Port:      8080,
		Protocol:  config.HTTPProto,
		Endpoints: eps,
	}
	backends = append(backends, backend)
	frontend := &config.FrontendService{
		Name:            "foo",
		Port:            80,
		Protocol:        config.HTTPSProto,
		BackendServices: backends,
	}

	frontends := []*config.FrontendService{}
	frontends = append(frontends, frontend)

	defCert := &config.Certificate{
		Name: "defaultCertificate",
		Cert: "------Begin Certificate-----",
		Key:  "------Begin Key-----",
	}

	lbConfig := &config.LoadBalancerConfig{
		FrontendServices: frontends,
		DefaultCert:      defCert,
	}
	defer os.RemoveAll(lbp.cfg.Config)
	err := lbp.cfg.write(lbConfig)

	if err != nil {
		t.Fatalf("Error while writing haproxy config: %v", err)
	}

	cfgFile := ""

	b, err := ioutil.ReadFile(lbp.cfg.Config)
	if err != nil {
		t.Fatalf("Error while reading the haproxy config file: %v", err)
	}
	cfgFile = string(b)

	//make sure the cfg file has default cert mentioned
	if !strings.Contains(cfgFile, "ssl crt /etc/haproxy/certs/current/defaultCertificate.pem ssl crt /etc/haproxy/certs/current") {
		t.Fatalf("Error validating default cert presence in haproxy config")
	}
}

func TestBuildCustomConfigBackendServerWeight(t *testing.T) {
	customConfig, err := getCustomConfig("custom_config_backend_server")
	if err != nil {
		t.Fatalf("Failed to read custom config: %v", err)
	}
	backends := []*config.BackendService{}
	var eps1 config.Endpoints
	ep1 := &config.Endpoint{
		Name: "drainedS1",
		IP:   "10.1.1.17",
		Port: 90,
	}
	eps1 = append(eps1, ep1)
	backend := &config.BackendService{
		UUID:      "foo",
		Port:      8080,
		Protocol:  config.HTTPProto,
		Endpoints: eps1,
	}
	backends = append(backends, backend)

	frontends := []*config.FrontendService{}
	frontend := &config.FrontendService{
		Name:            "foo",
		Port:            80,
		Protocol:        config.HTTPProto,
		BackendServices: backends,
	}
	frontends = append(frontends, frontend)

	lbConfig := &config.LoadBalancerConfig{
		FrontendServices: frontends,
	}
	err = lbp.ProcessCustomConfig(lbConfig, customConfig)
	if err != nil {
		t.Fatalf("Error while process custom config: %v", err)
	}

	result, err := validateCustomConfig("custom_config_server_weight_resp", lbConfig.FrontendServices[0].BackendServices[0].Endpoints[0].Config)
	if err != nil {
		t.Fatalf("Error validating custom config: %v", err)
	}

	if !result {
		t.Fatal("Configs don't match")
	}
}
