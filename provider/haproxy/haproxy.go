package haproxy

import (
	"bufio"
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/patrickmn/go-cache"
	"github.com/rancher/lb-controller/config"
	"github.com/rancher/lb-controller/provider"
	utils "github.com/rancher/lb-controller/utils"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
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

	dm := &drainMgr{
		drainList:   make(map[string]int),
		mu:          &sync.RWMutex{},
		haproxySock: "/var/run/haproxy.sock",
		pollResults: cache.New(5*time.Minute, 10*time.Minute),
	}

	lbp := Provider{
		cfg:      haproxyCfg,
		stopCh:   make(chan struct{}),
		init:     true,
		drainMgr: dm,
	}
	provider.RegisterProvider(lbp.GetName(), &lbp)
}

type Stat map[string]string

type StatsPollResult struct {
	PollCount int
	ScurValue string
	Weight    string
}

type Provider struct {
	cfg      *haproxyConfig
	stopCh   chan struct{}
	init     bool
	drainMgr *drainMgr
}

type haproxyConfig struct {
	Name      string
	ReloadCmd string
	StartCmd  string
	Config    string
	Template  string
	CertDir   string
}

type drainManager interface {
	AddEndpointForDrain(*config.Endpoint)
	isEndpointUpForDrain(*config.Endpoint) bool
}

type drainMgr struct {
	drainList   map[string]int
	mu          *sync.RWMutex
	haproxySock string
	pollResults *cache.Cache
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
	logrus.Info("Initializing conf")
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
			for _, ep := range be.Endpoints {
				logrus.Infof("backend ep name: %v, ip: %v, port: %v, config: %v ", ep.Name, ep.IP, ep.Port, ep.Config)
			}
		}
		frontends = append(frontends, fe)
	}
	conf["frontends"] = frontends
	conf["backends"] = backends
	conf["globalConfig"] = lbConfig.Config
	conf["strictSni"] = lbConfig.DefaultCert == nil
	if lbConfig.DefaultCert != nil {
		conf["defaultCertFile"] = fmt.Sprintf("%s.pem", lbConfig.DefaultCert.Name)
	}
	err = t.Execute(w, conf)
	logrus.Infof("backends size %v ", len(backends))
	for _, be := range backends {
		for _, ep := range be.Endpoints {
			logrus.Infof("After write backend ep name %v , ip %v: ", ep.Name, ep.IP, ep.Port)
		}
	}
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

func (lbp *Provider) IsEndpointUpForDrain(ep *config.Endpoint) bool {
	return lbp.drainMgr.isEndpointUpForDrain(ep)
}

func (lbp *Provider) DrainEndpoint(ep *config.Endpoint) {
	lbp.drainMgr.AddEndpointForDrain(ep)
}

func (lbp *Provider) IsEndpointDrained(ep *config.Endpoint) bool {
	inDrainList := lbp.drainMgr.isEndpointUpForDrain(ep)
	if inDrainList {
		logrus.Infof("EP %v found in drainlist", ep.Name)
		//check the pollResults
		if x, found := lbp.drainMgr.pollResults.Get(ep.Name); found {
			results := x.(*StatsPollResult)
			if results != nil {
				if (results.ScurValue == "0" && results.Weight == "0") || (results.PollCount >= 3) {
					//remove from drain
					logrus.Infof("EP %v removed from drainlist", ep.Name)
					lbp.drainMgr.RemoveEndpointFromDrain(ep)
					return true
				}
			}
		}
	}
	return false
}

func (dm *drainMgr) AddEndpointForDrain(ep *config.Endpoint) {
	if ep != nil && ep.Name != "" {
		dm.mu.Lock()
		dm.drainList[ep.Name] = 0
		dm.mu.Unlock()
	}
}

func (dm *drainMgr) RemoveEndpointFromDrain(ep *config.Endpoint) {
	if ep != nil && ep.Name != "" {
		dm.mu.Lock()
		delete(dm.drainList, ep.Name)
		dm.mu.Unlock()
	}
}

func (dm *drainMgr) isEndpointUpForDrain(ep *config.Endpoint) bool {
	if ep != nil && ep.Name != "" {
		dm.mu.RLock()
		defer dm.mu.RUnlock()
		if _, ok := dm.drainList[ep.Name]; ok {
			return true
		}
	}
	return false
}

func (dm *drainMgr) PollStats() {
	logrus.Info("Starting --- PollStats")

	for {
		if len(dm.drainList) != 0 {
			logrus.Debug("Start --- PollStats")
			stats, err := dm.ReadStats()
			if err != nil {
				logrus.Errorf("PollStats: Error %v reading haproxy stats on  %v", err, dm.haproxySock)
			} else {
				dm.mu.RLock()
				for epname := range dm.drainList {
					if endpointStat, ok := stats[epname]; ok {
						scur := endpointStat["scur"]
						weight := endpointStat["weight"]

						results := &StatsPollResult{}
						//check if earlier pollresults are in cache
						if x, found := dm.pollResults.Get(epname); found {
							results = x.(*StatsPollResult)
						}
						results.PollCount = results.PollCount + 1
						results.ScurValue = scur
						results.Weight = weight
						dm.pollResults.Set(epname, results, cache.DefaultExpiration)
						logrus.Infof("EP %v pollResults %v %v %v", epname, results.PollCount, results.ScurValue, results.Weight)
					}
				}
				dm.mu.RUnlock()
			}
			logrus.Debug("Done --- PollStats")
		}
		time.Sleep(time.Duration(5) * time.Second)
	}
}

func (dm *drainMgr) ReadStats() (map[string]Stat, error) {
	stats := make(map[string]Stat)

	lines, err := dm.runCommand("show stat")
	if err != nil {
		return nil, err
	}

	if len(lines) == 0 || !strings.HasPrefix(lines[0], "# ") {
		return nil, fmt.Errorf("Failed to find stats")
	}

	keys := strings.Split(strings.TrimPrefix(lines[0], "# "), ",")

	for _, line := range lines[1:] {
		if line == "" {
			continue
		}

		values := strings.Split(line, ",")
		if len(keys) != len(values) {
			logrus.Errorf("Invalid stat line: %s", line)
		}

		stat := Stat{}

		for i := 0; i < len(values); i++ {
			stat[keys[i]] = values[i]
		}

		stats[stat["svname"]] = stat
	}

	logrus.Infof("Returning Stats %v", stats)
	return stats, err
}

func (dm *drainMgr) runCommand(cmd string) ([]string, error) {
	conn, err := net.Dial("unix", dm.haproxySock)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	_, err = conn.Write([]byte(cmd + "\n"))
	if err != nil {
		return nil, err
	}

	result := []string{}
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		result = append(result, scanner.Text())
	}

	return result, scanner.Err()
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

func (lbp *Provider) StartStatsPoll() {
	logrus.Debug("Calling the PollStats")
	go lbp.drainMgr.PollStats()
}

func (lbp *Provider) Run(syncEndpointsQueue *utils.TaskQueue) {
	lbp.StartHaproxy()
	lbp.StartStatsPoll()
	lbp.init = false
	<-lbp.stopCh
}

func (lbp *Provider) Stop() error {
	logrus.Infof("Shutting down provider %v", lbp.GetName())
	close(lbp.stopCh)
	return nil
}

func (lbp *Provider) ProcessCustomConfig(lbConfig *config.LoadBalancerConfig, customConfig string) error {
	return BuildCustomConfig(lbConfig, customConfig, lbp.drainMgr)
}

func (lbp *Provider) CleanupConfig(name string) error {
	return nil
}
