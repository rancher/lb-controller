package provider

import (
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/rancher/lb-controller/config"
	"github.com/rancher/lb-controller/provider"
	utils "github.com/rancher/lb-controller/utils"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"text/template"
	"time"
)

func init() {
	haproxyCfg := &haproxyConfig{
		ReloadCmd: "haproxy_reload /etc/haproxy/haproxy.cfg reload",
		ResetCmd:  "haproxy_reload /etc/haproxy/haproxy.cfg reset",
		Config:    "/etc/haproxy/haproxy_new.cfg",
		Template:  "/etc/haproxy/haproxy_template.cfg",
		CertDir:   "/etc/haproxy/certs",
	}
	lbp := HAProxyProvider{
		cfg:    haproxyCfg,
		stopCh: make(chan struct{}),
		init:   true,
	}
	provider.RegisterProvider(lbp.GetName(), &lbp)
}

type HAProxyProvider struct {
	cfg    *haproxyConfig
	stopCh chan struct{}
	init   bool
}

type haproxyConfig struct {
	Name      string
	ReloadCmd string
	ResetCmd  string
	Config    string
	Template  string
	CertDir   string
}

func (cfg *haproxyConfig) write(lbConfig *config.LoadBalancerConfig) (err error) {
	var w io.Writer
	w, err = os.Create(cfg.Config)
	if err != nil {
		return err
	}
	var t *template.Template
	t, err = template.ParseFiles(cfg.Template)
	if err != nil {
		return err
	}
	conf := make(map[string]interface{})
	m := make(map[string]string)
	backends := []*config.BackendService{}
	frontends := []*config.FrontendService{}
	supportedProtos := map[string]bool{
		config.HTTPProto:  true,
		config.HTTPSProto: true,
		config.TLSProto:   true,
		config.TCPProto:   true,
	}
	for _, fe := range lbConfig.FrontendServices {
		//filter our based on supported proto
		if !supportedProtos[fe.Protocol] {
			continue
		}
		for _, be := range fe.BackendServices {
			if _, ok := m[be.UUID]; ok {
				continue
			}
			m[be.UUID] = be.UUID
			backends = append(backends, be)
		}
		frontends = append(frontends, fe)
	}
	conf["frontends"] = frontends
	conf["backends"] = backends
	conf["globalConfig"] = lbConfig.Config
	conf["strictSni"] = lbConfig.DefaultCert
	err = t.Execute(w, conf)
	return err
}

func (lbp *HAProxyProvider) applyHaproxyConfig(lbConfig *config.LoadBalancerConfig, reset bool) error {
	// copy certificates
	if _, err := os.Stat(lbp.cfg.CertDir); os.IsNotExist(err) {
		if err = os.Mkdir(lbp.cfg.CertDir, 0644); err != nil {
			return err
		}
	}
	currentCerts := fmt.Sprintf("%s/%s", lbp.cfg.CertDir, "current")
	if _, err := os.Stat(currentCerts); os.IsNotExist(err) {
		if err = os.Mkdir(currentCerts, 0644); err != nil {
			return err
		}
	}

	newCerts := fmt.Sprintf("%s/%s", lbp.cfg.CertDir, "new")
	if _, err := os.Stat(newCerts); os.IsNotExist(err) {
		if err = os.Mkdir(newCerts, 0644); err != nil {
			return err
		}
	}
	certs := []*config.Certificate{}
	if len(lbConfig.Certs) > 0 {
		certs = append(certs, lbConfig.Certs...)
	}
	if lbConfig.DefaultCert != nil {
		certs = append(certs, lbConfig.DefaultCert)
	}
	for _, cert := range certs {
		certStr := fmt.Sprintf("%s\n%s", cert.Key, cert.Cert)
		b := []byte(certStr)
		path := fmt.Sprintf("%s/%s.pem", newCerts, cert.Name)
		err := ioutil.WriteFile(path, b, 0644)
		if err != nil {
			return err
		}
	}
	// apply config
	if err := lbp.cfg.write(lbConfig); err != nil {
		return err
	}
	if reset {
		return lbp.cfg.reset()
	}
	return lbp.cfg.reload()
}

func (lbp *HAProxyProvider) ApplyConfig(lbConfig *config.LoadBalancerConfig) error {
	//check if the config is being resetting
	for i := 0; i < 5; i++ {
		if lbp.init {
			time.Sleep(time.Second * time.Duration(1))
			continue
		}
		return lbp.applyHaproxyConfig(lbConfig, false)
	}
	return fmt.Errorf("Failed to wait for %s to exit init stage", lbp.GetName())
}

func (lbp *HAProxyProvider) GetName() string {
	return "haproxy"
}

func (lbp *HAProxyProvider) GetPublicEndpoints(configName string) []string {
	epStr := []string{}
	return epStr
}

func (cfg *haproxyConfig) reset() error {
	output, err := exec.Command("sh", "-c", cfg.ResetCmd).CombinedOutput()
	msg := fmt.Sprintf("%v -- %v", cfg.Name, string(output))
	logrus.Info(msg)
	if err != nil {
		return fmt.Errorf("error restarting %v: %v", msg, err)
	}
	return nil
}

func (cfg *haproxyConfig) reload() error {
	output, err := exec.Command("sh", "-c", cfg.ReloadCmd).CombinedOutput()
	msg := fmt.Sprintf("%v -- %v", cfg.Name, string(output))
	logrus.Info(msg)
	if err != nil {
		return fmt.Errorf("error restarting %v: %v", msg, err)
	}
	return nil
}

func (lbp *HAProxyProvider) CleanupConfig(name string) error {
	return lbp.applyHaproxyConfig(&config.LoadBalancerConfig{}, true)
}

func (lbp *HAProxyProvider) IsHealthy() bool {
	return true
}

func (lbp *HAProxyProvider) Run(syncEndpointsQueue *utils.TaskQueue) {
	// cleanup the config
	lbp.CleanupConfig("")
	lbp.init = false
	<-lbp.stopCh
}

func (lbp *HAProxyProvider) Stop() error {
	logrus.Infof("Shutting down provider %v", lbp.GetName())
	close(lbp.stopCh)
	return lbp.CleanupConfig("")
}

func (lbp *HAProxyProvider) ProcessCustomConfig(lbConfig *config.LoadBalancerConfig, customConfig string) error {
	return BuildCustomConfig(lbConfig, customConfig)
}
