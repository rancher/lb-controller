package lbprovider

import (
	"fmt"
	"github.com/golang/glog"
	"github.com/rancher/rancher-ingress/lbconfig"
	"io"
	"os"
	"os/exec"
	"text/template"
)

func init() {
	var config string
	if config = os.Getenv("HAPROXY_CONFIG"); len(config) == 0 {
		glog.Info("HAPROXY_CONFIG is not set, skipping init of haproxy provider")
		return
	}
	haproxyCfg := &haproxyConfig{
		ReloadCmd: "haproxy_reload",
		Config:    config,
		Template:  "/etc/haproxy/haproxy_template.cfg",
	}
	lbp := HAProxyProvider{
		cfg: haproxyCfg,
	}
	RegisterProvider(lbp.GetName(), &lbp)
}

type HAProxyProvider struct {
	cfg *haproxyConfig
}

type haproxyConfig struct {
	Name      string
	ReloadCmd string
	Config    string
	Template  string
}

func (cfg *haproxyConfig) write(lbConfig *lbconfig.LoadBalancerConfig) (err error) {
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
	conf["frontends"] = lbConfig.FrontendServices
	err = t.Execute(w, conf)
	return err
}

func (lbc *HAProxyProvider) ApplyConfig(lbName string, lbConfig *lbconfig.LoadBalancerConfig) error {
	if err := lbc.cfg.write(lbConfig); err != nil {
		return err
	}
	return lbc.cfg.reload()
}

func (lbc *HAProxyProvider) GetName() string {
	return "haproxy"
}

func (lbc *HAProxyProvider) GetPublicEndpoint(lbName string) string {
	return Localhost
}

func (cfg *haproxyConfig) reload() error {
	output, err := exec.Command("sh", "-c", cfg.ReloadCmd).CombinedOutput()
	msg := fmt.Sprintf("%v -- %v", cfg.Name, string(output))
	if err != nil {
		return fmt.Errorf("error restarting %v: %v", msg, err)
	}
	return nil
}
