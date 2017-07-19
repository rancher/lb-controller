package haproxy

import (
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/rancher/lb-controller/config"
	"github.com/rancher/lb-controller/provider"
	utils "github.com/rancher/lb-controller/utils"
	"github.com/urfave/cli"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"text/template"
	"time"
)

func init() {
	haproxyCfg := &haproxyConfig{
		ReloadCmd: "haproxy_reload /etc/haproxy/haproxy.cfg reload",
		StartCmd:  "haproxy_reload /etc/haproxy/haproxy.cfg start",
		Config:    "/etc/haproxy/haproxy_new.cfg",
		Template:  "/etc/haproxy/haproxy_template.cfg",
		CertDir:   "/etc/haproxy/certs",
	}
	lbp := Provider{
		cfg:    haproxyCfg,
		stopCh: make(chan struct{}),
		init:   true,
	}
	provider.RegisterProvider(lbp.GetName(), &lbp)
}

type Provider struct {
	cfg    *haproxyConfig
	stopCh chan struct{}
	init   bool
}

type haproxyConfig struct {
	Name      string
	ReloadCmd string
	StartCmd  string
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
		config.SNIProto:   true,
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
	conf["strictSni"] = lbConfig.DefaultCert == nil
	if lbConfig.DefaultCert != nil {
		defCertName := strings.Replace(lbConfig.DefaultCert.Name, " ", "\\ ", -1)
		conf["defaultCertFile"] = fmt.Sprintf("%s.pem", defCertName)
	}
	err = t.Execute(w, conf)
	return err
}

func (lbp *Provider) applyHaproxyConfig(lbConfig *config.LoadBalancerConfig) error {
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
	if lbConfig.DefaultCert != nil {
		certs = append(certs, lbConfig.DefaultCert)
	}
	if len(lbConfig.Certs) > 0 {
		certs = append(certs, lbConfig.Certs...)
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

	return lbp.cfg.reload()
}

func (lbp *Provider) ApplyConfig(lbConfig *config.LoadBalancerConfig) error {
	//check if the config is being starting
	for i := 0; i < 5; i++ {
		if lbp.init {
			time.Sleep(time.Second * time.Duration(1))
			continue
		}
		return lbp.applyHaproxyConfig(lbConfig)
	}
	return fmt.Errorf("Failed to wait for %s to exit init stage", lbp.GetName())
}

func (lbp *Provider) GetName() string {
	return "haproxy"
}

func (lbp *Provider) GetPublicEndpoints(configName string) []string {
	epStr := []string{}
	return epStr
}

func (cfg *haproxyConfig) start() error {
	output, err := exec.Command("sh", "-c", cfg.StartCmd).CombinedOutput()
	msg := fmt.Sprintf("%v -- %v", cfg.Name, string(output))
	if string(output) != "" {
		logrus.Info(msg)
	}
	if err != nil {
		return fmt.Errorf("error starting %v: %v", msg, err)
	}
	return nil
}

func (cfg *haproxyConfig) reload() error {
	output, err := exec.Command("sh", "-c", cfg.ReloadCmd).CombinedOutput()
	msg := fmt.Sprintf("%v -- %v", cfg.Name, string(output))
	if string(output) != "" {
		logrus.Info(msg)
	}
	if err != nil {
		return fmt.Errorf("error reloading %v: %v", msg, err)
	}
	return nil
}

func (lbp *Provider) StartHaproxy() error {
	return lbp.cfg.start()
}

func (lbp *Provider) IsHealthy() bool {
	return true
}

func (lbp *Provider) Run(syncEndpointsQueue *utils.TaskQueue) {
	lbp.StartHaproxy()
	lbp.init = false
	<-lbp.stopCh
}

func (lbp *Provider) Stop() error {
	logrus.Infof("Shutting down provider %v", lbp.GetName())
	close(lbp.stopCh)
	return nil
}

func (lbp *Provider) ProcessCustomConfig(lbConfig *config.LoadBalancerConfig, customConfig string) error {
	return BuildCustomConfig(lbConfig, customConfig)
}

func (lbp *Provider) CleanupConfig(name string) error {
	return nil
}

func (lbp *Provider) Init(c *cli.Context) {
}
