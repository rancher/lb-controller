package lbprovider

import (
	"fmt"
	"github.com/golang/glog"
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
	lbConfig := &loadBalancerConfig{
		ReloadCmd: "haproxy_reload",
		Config:    config,
		Template:  "/etc/haproxy/haproxy_template.cfg",
	}
	lbp := HAProxyProvider{
		cfg: lbConfig,
	}
	RegisterProvider(lbp.GetName(), &lbp)
}

type HAProxyProvider struct {
	cfg *loadBalancerConfig
}

type loadBalancerConfig struct {
	Name      string
	ReloadCmd string
	Config    string
	Template  string
}

func (cfg *loadBalancerConfig) write() (err error) {
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
	//FIXME - add real content
	conf := make(map[string]interface{})
	err = t.Execute(w, conf)
	return err
}

func (lbc *HAProxyProvider) ApplyConfig() error {
	if err := lbc.cfg.write(); err != nil {
		return err
	}
	return lbc.cfg.reload()
}

func (lbc *HAProxyProvider) GetName() string {
	return "haproxy"
}

func (cfg *loadBalancerConfig) reload() error {
	output, err := exec.Command("sh", "-c", cfg.ReloadCmd).CombinedOutput()
	msg := fmt.Sprintf("%v -- %v", cfg.Name, string(output))
	if err != nil {
		return fmt.Errorf("error restarting %v: %v", msg, err)
	}
	glog.Infof(msg)
	return nil
}
