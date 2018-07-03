package haproxy

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/mitchellh/go-ps"
	"github.com/rancher/lb-controller/config"
	"github.com/rancher/lb-controller/provider"
	utils "github.com/rancher/lb-controller/utils"
	"github.com/rancher/log"
)

func init() {
	haproxyCfg := &haproxyConfig{
		ReloadCmd: "haproxy_reload /etc/haproxy/haproxy.cfg reload",
		StartCmd:  "haproxy_reload /etc/haproxy/haproxy.cfg start",
		Config:    "/etc/haproxy/haproxy_new.cfg",
		Template:  "/etc/haproxy/haproxy_template.cfg",
		CertDir:   "/etc/haproxy/certs",
		PidFile:   "/run/haproxy.pid",
	}

	dm := &drainMgr{
		drainList:   make(map[string]string),
		mu:          &sync.RWMutex{},
		haproxySock: "/var/run/haproxy.sock",
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
	PidFile   string
}

type drainManager interface {
	AddEndpointForDrain(*config.Endpoint, string) bool
	isEndpointUpForDrain(*config.Endpoint) (bool, string)
}

type drainMgr struct {
	drainList   map[string]string
	mu          *sync.RWMutex
	haproxySock string
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

func (cfg *haproxyConfig) readPid() (string, error) {
	content, err := ioutil.ReadFile(cfg.PidFile)
	if err != nil {
		return "", err
	}
	pid := string(content)
	pid = strings.TrimSuffix(pid, "\n")
	return pid, err
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

func (lbp *Provider) GetPublicEndpoints(configName string) ([]string, error) {
	return []string{}, nil
}

func (lbp *Provider) IsEndpointUpForDrain(ep *config.Endpoint) bool {
	isInDrain, _ := lbp.drainMgr.isEndpointUpForDrain(ep)
	return isInDrain
}

func (lbp *Provider) DrainEndpoint(ep *config.Endpoint) bool {
	pid, err := lbp.cfg.readPid()

	if err != nil {
		log.Errorf("Error while reading haproxy pid %v", err)
		return false
	}
	return lbp.drainMgr.AddEndpointForDrain(ep, pid)
}

func (lbp *Provider) RemoveEndpointFromDrain(ep *config.Endpoint) {
	lbp.drainMgr.RemoveEndpointFromDrain(ep)
}

func (lbp *Provider) IsEndpointDrained(ep *config.Endpoint) bool {
	inDrainList, pid := lbp.drainMgr.isEndpointUpForDrain(ep)
	if inDrainList {
		//check if the pid is the current pid
		// if yes check stats on socket and see scur, scur = 0 drained.
		// if no check if any old pid exists - if yes not drained yet. If no, drained.
		currentPid, err := lbp.cfg.readPid()
		if err != nil {
			log.Errorf("Error while reading haproxy pid %v", err)
			return false
		}
		if pid != currentPid {
			if pid != "" {
				pidInt, err := strconv.Atoi(pid)
				if err != nil {
					log.Errorf("Error %v while converting pid %v", err, pid)
				} else {
					process, err := ps.FindProcess(pidInt)
					if err != nil {
						log.Errorf("Error %v while listing haproxy process by pid %v", err, pid)
					}
					if err == nil && process == nil {
						return true
					}
					log.Infof("IsEndpointDrained: old haproxy process found pid %v, name %v", pid, process.Executable())
				}
			} else {
				//check if current pid stats are scur = 0 AND no other pid exists.
				//read stats
				scur, _, err := lbp.drainMgr.readCurrentStats(ep)
				if err != nil {
					log.Errorf("IsEndpointDrained: Error %v", err)
					return false
				}
				log.Debugf("IsEndpointDrained: scur %v Endpoint %v", scur, ep.Name)
				oldPidExists, err := lbp.drainMgr.olderPidExists()
				if err != nil {
					log.Errorf("AddEndpointForDrain: Error %v listing older haproxy processes, drain till timeout %v", err, ep.DrainTimeout)
					return false
				}
				if !oldPidExists && scur == "0" {
					return true
				}
			}
		} else {
			//read stats
			scur, _, err := lbp.drainMgr.readCurrentStats(ep)
			if err != nil {
				log.Errorf("IsEndpointDrained: Error %v", err)
				return false
			}
			log.Debugf("IsEndpointDrained: scur %v Endpoint %v", scur, ep.Name)
			if scur == "0" {
				return true
			}
		}
	}
	return false
}

func (dm *drainMgr) readCurrentStats(ep *config.Endpoint) (string, []string, error) {
	totalScur := 0
	var backendNames []string
	//read stats
	stats, err := dm.ReadStats()
	if err != nil {
		return "", backendNames, fmt.Errorf("Error %v reading haproxy stats on  %v", err, dm.haproxySock)
	}

	if _, ok := stats[ep.Name]; !ok {
		return "", backendNames, fmt.Errorf("Error %v finding endpoint %v in haproxy stats", err, ep.Name)
	}

	for _, endpointStat := range stats[ep.Name] {
		log.Debugf("scur for %v is: %v", endpointStat["pxname"]+"/"+ep.Name, endpointStat["scur"])
		if escur, ok := endpointStat["scur"]; ok {
			if escur != "" {
				escurInt, err := strconv.Atoi(escur)
				if err != nil {
					log.Errorf("Error %v while converting scur %v, defaulting to 0", err, escur)
					escurInt = 0
				}
				totalScur = totalScur + escurInt
			}
		}
		if pxname, ok := endpointStat["pxname"]; ok {
			endpointFullName := pxname + "/" + ep.Name
			backendNames = append(backendNames, endpointFullName)
		}
	}

	return strconv.Itoa(totalScur), backendNames, nil

}

func (dm *drainMgr) olderPidExists() (bool, error) {
	output, err := exec.Command("pidof", "haproxy").Output()
	if err != nil {
		return true, fmt.Errorf("error listing old process %v", err)
	}
	processes := string(output)
	if string(output) != "" {
		log.Debugf("Output of old process %v", processes)
	}

	parts := strings.Split(processes, " ")
	if len(parts) > 1 {
		return true, nil
	}
	return false, nil
}

func (dm *drainMgr) AddEndpointForDrain(ep *config.Endpoint, pid string) bool {
	//check if drain is needed by looking at Stats
	if ep != nil && ep.Name != "" {
		scur, pxnames, err := dm.readCurrentStats(ep)
		if err != nil {
			log.Errorf("AddEndpointForDrain: Error %v", err)
			return false
		}
		//set weight to zero on the socket anyway to stop accepting new connections
		setWeightFailed := false
		for _, pxname := range pxnames {
			err = dm.SetWeightForDrain(pxname)
			if err != nil {
				log.Errorf("AddEndpointForDrain: Error %v setting weight via socket for Endpoint backend %v", err, pxname)
				setWeightFailed = true
			}
		}

		if setWeightFailed {
			return false
		}

		log.Debugf("AddEndpointForDrain: scur %v Endpoint %v", scur, ep.Name)
		if scur == "0" {
			//stats are zero on the current pid, check if any older pid around.
			oldPidExists, err := dm.olderPidExists()
			if err != nil {
				log.Errorf("AddEndpointForDrain: Error %v listing older haproxy processes, drain till timeout %v", err, ep.DrainTimeout)
			}
			log.Debugf("AddEndpointForDrain: pid %v oldpid %v", pid, oldPidExists)

			if !oldPidExists {
				log.Debugf("AddEndpointForDrain: Drain not needed for Endpoint %v", ep.Name)
				return false
			}
			pid = ""
		}
		// add to drainList
		dm.mu.Lock()
		dm.drainList[ep.Name] = pid
		dm.mu.Unlock()
		return true
	}
	return false
}

func (dm *drainMgr) RemoveEndpointFromDrain(ep *config.Endpoint) {
	if ep != nil && ep.Name != "" {
		dm.mu.Lock()
		delete(dm.drainList, ep.Name)
		dm.mu.Unlock()
		log.Info("Removed Endpoint %v From Drain", ep)
	}
}

func (dm *drainMgr) isEndpointUpForDrain(ep *config.Endpoint) (bool, string) {
	if ep != nil && ep.Name != "" {
		dm.mu.RLock()
		defer dm.mu.RUnlock()
		if t, ok := dm.drainList[ep.Name]; ok {
			return true, t
		}
	}
	return false, ""
}

func (dm *drainMgr) ReadStats() (map[string][]Stat, error) {
	stats := make(map[string][]Stat)

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
			log.Errorf("Invalid stat line: %s", line)
		}

		stat := Stat{}

		for i := 0; i < len(values); i++ {
			stat[keys[i]] = values[i]
		}

		stats[stat["svname"]] = append(stats[stat["svname"]], stat)
	}

	return stats, err
}

func (dm *drainMgr) SetWeightForDrain(backend string) error {
	_, err := dm.runCommand("set server " + backend + " weight 0")
	if err != nil {
		return err
	}
	return nil
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
		log.Info(msg)
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
		log.Info(msg)
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
	log.Infof("Shutting down provider %v", lbp.GetName())
	close(lbp.stopCh)
	return nil
}

func (lbp *Provider) ProcessCustomConfig(lbConfig *config.LoadBalancerConfig, customConfig string) error {
	return BuildCustomConfig(lbConfig, customConfig, lbp.drainMgr)
}

func (lbp *Provider) CleanupConfig(name string) error {
	return nil
}
