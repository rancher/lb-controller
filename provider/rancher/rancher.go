package rancher

import (
	"encoding/json"
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/rancher/event-subscriber/locks"
	"github.com/rancher/go-rancher/client"
	"github.com/rancher/lb-controller/config"
	"github.com/rancher/lb-controller/provider"
	utils "github.com/rancher/lb-controller/utils"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type PublicEndpoint struct {
	IPAddress string
	Port      int
}

const (
	controllerStackName        string = "kubernetes-ingress-lbs"
	controllerExternalIDPrefix string = "kubernetes-ingress-lbs://"
	lbSvcNameSeparator         string = "-rancherlb-"
)

type LBProvider struct {
	client             *client.RancherClient
	opts               *client.ClientOpts
	syncEndpointsQueue *utils.TaskQueue
	stopCh             chan struct{}
}

func init() {
	cattleURL := os.Getenv("CATTLE_URL")
	if len(cattleURL) == 0 {
		logrus.Info("CATTLE_URL is not set, skipping init of Rancher LB provider")
		return
	}

	cattleAccessKey := os.Getenv("CATTLE_ACCESS_KEY")
	if len(cattleAccessKey) == 0 {
		logrus.Info("CATTLE_ACCESS_KEY is not set, skipping init of Rancher LB provider")
		return
	}

	cattleSecretKey := os.Getenv("CATTLE_SECRET_KEY")
	if len(cattleSecretKey) == 0 {
		logrus.Info("CATTLE_SECRET_KEY is not set, skipping init of Rancher LB provider")
		return
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

	lbp := &LBProvider{
		client: client,
		opts:   opts,
		stopCh: make(chan struct{}),
	}

	provider.RegisterProvider(lbp.GetName(), lbp)
}

func (lbp *LBProvider) IsHealthy() bool {
	_, err := lbp.client.Environment.List(client.NewListOpts())
	if err != nil {
		logrus.Errorf("Health check failed: unable to reach Rancher. Error: %#v", err)
		return false
	}
	return true
}

func (lbp *LBProvider) lockLB(lbName string) (locks.Unlocker, error) {
	var unlocker locks.Unlocker
	for i := 0; i < 10; i++ {
		unlocker = locks.Lock(lbName)
		if unlocker != nil {
			break
		}
		time.Sleep(time.Second * time.Duration(1))
	}

	if unlocker == nil {
		return nil, fmt.Errorf("Failed to acquire lock on lb [%s]", lbName)
	}
	return unlocker, nil
}

func (lbp *LBProvider) ApplyConfig(lbConfig *config.LoadBalancerConfig) error {
	unlocker, err := lbp.lockLB(lbConfig.Name)
	if err != nil {
		return err
	}

	defer unlocker.Unlock()

	// 1.create serivce
	lb, err := lbp.createLBService(lbConfig)
	if err != nil {
		return err
	}

	// 2.update with certificate if needed
	if err = lbp.updateCertificate(lbConfig, lb); err != nil {
		return err
	}

	// 3.set service links
	if err = lbp.setServiceLinks(lb, lbConfig); err != nil {
		return err
	}
	return nil
}

func (lbp *LBProvider) CleanupConfig(name string) error {
	lb, err := lbp.getLBServiceForConfig(name)
	if err != nil {
		return err
	}
	if lb == nil {
		logrus.Infof("LB [%s] doesn't exist, no need to cleanup ", name)
		return nil
	}
	logrus.Infof("Deleting lb service [%s]", lb.Name)
	return lbp.deleteLBService(lb, false)
}

func (lbp *LBProvider) Stop() error {
	close(lbp.stopCh)
	logrus.Infof("shutting down syncEndpointsQueue")
	lbp.syncEndpointsQueue.Shutdown()
	return nil
}

func (lbp *LBProvider) Run(syncEndpointsQueue *utils.TaskQueue) {
	lbp.syncEndpointsQueue = syncEndpointsQueue
	go lbp.syncEndpointsQueue.Run(time.Second, lbp.stopCh)

	go lbp.syncupEndpoints()

	<-lbp.stopCh
	logrus.Infof("shutting down kubernetes-lb-controller")
}

func (lbp *LBProvider) syncupEndpoints() error {
	// FIXME - change to listen to state.change events
	// figure out why events weren't received by this agent account
	for {
		time.Sleep(30 * time.Second)
		//get all lb services in the system
		lbs, err := lbp.getAllLBServices()
		if err != nil {
			logrus.Errorf("Failed to get lb services: %v", err)
			continue
		}
		for _, lb := range lbs {
			splitted := strings.SplitN(lb.Name, lbSvcNameSeparator, 2)
			if len(splitted) != 2 {
				// to support legacy code when we used "-"" as a separator
				splitted = strings.SplitN(lb.Name, "-", 2)
			}
			// handle the case when lb was created outside of ingress scope
			if len(splitted) < 2 {
				continue
			}
			lbp.syncEndpointsQueue.Enqueue(fmt.Sprintf("%v/%v", splitted[0], splitted[1]))
		}
	}
}

func (lbp *LBProvider) deleteLBService(lb *client.LoadBalancerService, waitForRemoval bool) error {
	_, err := lbp.client.LoadBalancerService.ActionRemove(lb)
	if err != nil {
		return err
	}

	if !waitForRemoval {
		return nil
	}

	lb, err = lbp.reloadLBService(lb)
	if err != nil {
		return err
	}

	actionChannel := lbp.waitForLBAction("purge", lb)
	_, ok := <-actionChannel
	if !ok {
		return fmt.Errorf("Failed to finish remove on lb [%s]. LB state: [%s]. LB status: [%s]", lb.Name, lb.State, lb.TransitioningMessage)
	}
	return err
}

func (lbp *LBProvider) formatLBName(name string, legacy bool) string {
	if legacy {
		return strings.Replace(name, "/", "-", -1)
	}
	return strings.Replace(name, "/", lbSvcNameSeparator, -1)
}

func (lbp *LBProvider) GetName() string {
	return "rancher"
}

func (lbp *LBProvider) GetPublicEndpoints(configName string) []string {
	epStr := []string{}
	lb, err := lbp.getLBServiceForConfig(configName)
	if err != nil {
		logrus.Errorf("Failed to find LB [%s]: %v", configName, err)
		return epStr
	}
	if lb == nil {
		logrus.Infof("LB [%s] is not ready yet, skipping endpoint update", configName)
		return epStr
	}

	epChannel := lbp.waitForLBPublicEndpoints(1, lb)
	_, ok := <-epChannel
	if !ok {
		logrus.Infof("Couldn't get publicEndpoints for LB [%s], skipping endpoint update", lb.Name)
		return epStr
	}

	lb, err = lbp.reloadLBService(lb)
	if err != nil {
		logrus.Infof("Failed to reload LB [%s], skipping endpoint update", lb.Name)
		return epStr
	}

	eps := lb.PublicEndpoints
	if len(eps) == 0 {
		logrus.Infof("No public endpoints found for LB [%s], skipping endpoint update", lb.Name)
		return epStr
	}

	for _, epObj := range eps {
		ep := PublicEndpoint{}

		err = convertObject(epObj, &ep)
		if err != nil {
			logrus.Errorf("Faield to convert public endpoints for LB [%s], skipping endpoint update %v", lb.Name, err)
			return epStr
		}
		epStr = append(epStr, ep.IPAddress)
	}

	return epStr
}

func convertObject(obj1 interface{}, obj2 interface{}) error {
	b, err := json.Marshal(obj1)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(b, obj2); err != nil {
		return err
	}
	return nil
}

type waitCallback func(result chan<- interface{}) (bool, error)

func (lbp *LBProvider) getOrCreateSystemStack() (*client.Environment, error) {
	opts := client.NewListOpts()
	opts.Filters["name"] = controllerStackName
	opts.Filters["removed_null"] = "1"
	opts.Filters["externalId"] = controllerExternalIDPrefix

	envs, err := lbp.client.Environment.List(opts)
	if err != nil {
		return nil, fmt.Errorf("Coudln't get stack by name [%s]. Error: %#v", controllerStackName, err)
	}

	if len(envs.Data) >= 1 {
		return &envs.Data[0], nil
	}

	env := &client.Environment{
		Name:       controllerStackName,
		ExternalId: controllerExternalIDPrefix,
	}

	env, err = lbp.client.Environment.Create(env)
	if err != nil {
		return nil, fmt.Errorf("Couldn't create ingress controller stack [%s]. Error: %#v", controllerStackName, err)
	}
	return env, nil
}

func (lbp *LBProvider) getStack(name string) (*client.Environment, error) {
	opts := client.NewListOpts()

	opts.Filters["externalId"] = fmt.Sprintf("kubernetes://%s", name)
	opts.Filters["removed_null"] = "1"

	envs, err := lbp.client.Environment.List(opts)
	if err != nil {
		return nil, fmt.Errorf("Coudln't get stack by name [%s]. Error: %#v", name, err)
	}

	if len(envs.Data) >= 1 {
		return &envs.Data[0], nil
	}
	return nil, nil
}

func (lbp *LBProvider) createCertificate(cert *config.Certificate) (*client.Certificate, error) {
	rancherCert := &client.Certificate{
		Name: cert.Name,
		Key:  cert.Key,
		Cert: cert.Cert,
	}

	rancherCert, err := lbp.client.Certificate.Create(rancherCert)
	if err != nil {
		return nil, fmt.Errorf("Unable to create certificate [%s]. Error: %#v", cert.Name, err)
	}
	return rancherCert, nil
}

func (lbp *LBProvider) updateCertificate(lbConfig *config.LoadBalancerConfig, lb *client.LoadBalancerService) error {
	rancherCertID, err := lbp.getRancherCertID(lbConfig)
	if err != nil {
		return err
	}
	if lb.DefaultCertificateId != rancherCertID {
		lb.DefaultCertificateId = rancherCertID
		logrus.Infof("Updating Rancher LB with the new cert [%s] ", rancherCertID)
		_, err = lbp.client.LoadBalancerService.Update(lb, map[string]interface{}{
			"defaultCertificateId": rancherCertID,
		})
		if err != nil {
			return fmt.Errorf("Failed to update lb [%s] with a new certificate [%s]. Error: %#v", lb.Name, rancherCertID, err)
		}
	}
	return nil
}

func (lbp *LBProvider) cleanupLBService(lb *client.LoadBalancerService, lbConfig *config.LoadBalancerConfig) *client.LoadBalancerService {
	if lb == nil {
		return nil
	}
	// check if service needs to be re-created
	// (when ports don't match)
	oldPorts := []string{}
	for _, port := range lb.LaunchConfig.Ports {
		split := strings.Split(port, ":")
		oldPorts = append(oldPorts, split[0])
	}

	newPorts := []string{}
	for _, frontEnd := range lbConfig.FrontendServices {
		newPorts = append(newPorts, strconv.Itoa(frontEnd.Port))
	}

	if portsChanged(newPorts, oldPorts) {
		logrus.Infof("Ports changed for LB service [%s], need to recreate", lb.Name)
		lbp.deleteLBService(lb, true)
		return nil
	}

	return lb
}

func (lbp *LBProvider) getLBServiceForConfig(lbConfigName string) (*client.LoadBalancerService, error) {
	fmtName := lbp.formatLBName(lbConfigName, false)
	lb, err := lbp.getLBServiceByName(fmtName)
	if err != nil {
		return nil, err
	}

	if lb != nil {
		return lb, nil
	}
	// legacy code where "-" was used as a separator
	fmtName = lbp.formatLBName(lbConfigName, true)
	logrus.Infof("Fetching service by name %v", fmtName)
	return lbp.getLBServiceByName(fmtName)
}

func portsChanged(newPorts []string, oldPorts []string) bool {
	if len(newPorts) != len(oldPorts) {
		return true
	}

	if len(newPorts) == 0 {
		return false
	}

	sort.Strings(newPorts)
	sort.Strings(oldPorts)
	for idx, p := range newPorts {
		if p != oldPorts[idx] {
			return true
		}
	}

	return false
}

func (lbp *LBProvider) createLBService(lbConfig *config.LoadBalancerConfig) (*client.LoadBalancerService, error) {
	stack, err := lbp.getOrCreateSystemStack()
	if err != nil {
		return nil, err
	}
	lb, err := lbp.getLBServiceForConfig(lbConfig.Name)
	if err != nil {
		return nil, err
	}

	lb = lbp.cleanupLBService(lb, lbConfig)

	if lb != nil {
		if lb.State != "active" {
			return lbp.activateLBService(lb)
		}
		return lb, nil
	}

	// private port will be overritten by ports
	// in hostname routing rules
	lbPorts := []string{}
	labels := make(map[string]interface{})
	for _, lbFrontend := range lbConfig.FrontendServices {
		defaultBackend := lbp.getDefaultBackend(lbFrontend)
		publicPort := strconv.Itoa(lbFrontend.Port)
		privatePort := strconv.Itoa(lbFrontend.Port)
		if defaultBackend != nil {
			privatePort = strconv.Itoa(defaultBackend.Port)
		}
		lbPorts = append(lbPorts, fmt.Sprintf("%v:%v", publicPort, privatePort))
		if lbFrontend.Protocol == config.HTTPSProto {
			labels["io.rancher.loadbalancer.ssl.ports"] = publicPort
		}
	}

	// get certificate from rancher
	rancherCertID, err := lbp.getRancherCertID(lbConfig)
	if err != nil {
		return nil, err
	}
	name := lbp.formatLBName(lbConfig.Name, false)
	lb = &client.LoadBalancerService{
		Name:          name,
		EnvironmentId: stack.Id,
		LaunchConfig: &client.LaunchConfig{
			Ports:  lbPorts,
			Labels: labels,
		},
		ExternalId:           fmt.Sprintf("%v%v", controllerExternalIDPrefix, name),
		DefaultCertificateId: rancherCertID,
		Scale:                int64(lbConfig.Scale),
	}

	lb, err = lbp.client.LoadBalancerService.Create(lb)
	if err != nil {
		return nil, fmt.Errorf("Unable to create LB [%s]. Error: %#v", name, err)
	}

	return lbp.activateLBService(lb)
}

func (lbp *LBProvider) getRancherCertID(lbConfig *config.LoadBalancerConfig) (string, error) {
	var defaultCert *config.Certificate
	for _, lbFrontend := range lbConfig.FrontendServices {
		if lbFrontend.DefaultCert != nil {
			defaultCert = lbFrontend.DefaultCert
		}
	}
	// get certificate
	var rancherCertID string
	if defaultCert != nil {
		rancherCert, err := lbp.getCertificate(defaultCert.Name)
		if err != nil {
			return "", fmt.Errorf("Failed to list certificate by name [%s]: %v", defaultCert.Name, err)
		}
		if rancherCert == nil {
			if defaultCert.Fetch {
				return "", fmt.Errorf("Failed to fetch certificate by name [%s]", defaultCert.Name)
			}
			// create certificate
			rancherCert, err = lbp.createCertificate(defaultCert)
			if err != nil {
				return "", fmt.Errorf("Failed to create certificate [%s]: %v", defaultCert.Name, err)
			}
		}
		rancherCertID = rancherCert.Id
	}
	return rancherCertID, nil
}

func (lbp *LBProvider) getDefaultBackend(frontend *config.FrontendService) *config.BackendService {
	for _, backend := range frontend.BackendServices {
		if backend.Path == "" && backend.Host == "" {
			return backend
		}
	}
	return nil
}

func (lbp *LBProvider) getCertificate(certName string) (*client.Certificate, error) {
	opts := client.NewListOpts()
	opts.Filters["name"] = certName
	opts.Filters["removed_null"] = "1"

	certs, err := lbp.client.Certificate.List(opts)
	if err != nil {
		return nil, fmt.Errorf("Coudln't get certificate by name [%s]. Error: %#v", certName, err)
	}

	if len(certs.Data) >= 1 {
		return &certs.Data[0], nil
	}
	return nil, nil
}

func (lbp *LBProvider) setServiceLinks(lb *client.LoadBalancerService, lbConfig *config.LoadBalancerConfig) error {
	if len(lbConfig.FrontendServices) == 0 {
		logrus.Infof("Config [%s] doesn't have any rules defined", lbConfig.Name)
		return nil
	}
	actionChannel := lbp.waitForLBAction("setservicelinks", lb)
	_, ok := <-actionChannel
	if !ok {
		return fmt.Errorf("Couldn't call setservicelinks on LB [%s]", lb.Name)
	}

	lb, err := lbp.reloadLBService(lb)
	if err != nil {
		return err
	}
	serviceLinks := &client.SetLoadBalancerServiceLinksInput{}

	for _, bcknd := range lbConfig.FrontendServices[0].BackendServices {
		svc, err := lbp.getKubernetesServiceByUUID(bcknd.UUID)
		if err != nil {
			return err
		}
		if svc == nil {
			return fmt.Errorf("Failed to find service [%s] in Rancher", bcknd.UUID)
		}
		ports := []string{}
		var port string
		bckndPort := strconv.Itoa(bcknd.Port)
		if bcknd.Host != "" && bcknd.Path != "" {
			port = fmt.Sprintf("%s%s=%s", bcknd.Host, bcknd.Path, bckndPort)
		} else if bcknd.Host != "" {
			port = fmt.Sprintf("%s=%s", bcknd.Host, bckndPort)
		} else if bcknd.Path != "" {
			port = fmt.Sprintf("%s=%s", bcknd.Path, bckndPort)
		}
		ports = append(ports, port)

		link := &client.LoadBalancerServiceLink{
			ServiceId: svc.Id,
			Ports:     ports,
		}
		serviceLinks.ServiceLinks = append(serviceLinks.ServiceLinks, link)

	}

	_, err = lbp.client.LoadBalancerService.ActionSetservicelinks(lb, serviceLinks)
	if err != nil {
		return fmt.Errorf("Failed to set service links for lb [%s]. Error: %#v", lb.Name, err)
	}
	return nil
}

func (lbp *LBProvider) activateLBService(lb *client.LoadBalancerService) (*client.LoadBalancerService, error) {
	// activate LB
	actionChannel := lbp.waitForLBAction("activate", lb)
	_, ok := <-actionChannel
	if !ok {
		return nil, fmt.Errorf("Couldn't call activate on LB [%s]. LB state: [%s]. LB status: [%s]", lb.Name, lb.State, lb.TransitioningMessage)
	}
	lb, err := lbp.reloadLBService(lb)
	if err != nil {
		return nil, err
	}
	_, err = lbp.client.LoadBalancerService.ActionActivate(lb)
	if err != nil {
		return nil, fmt.Errorf("Error creating LB [%s]. Couldn't activate LB. Error: %#v", lb.Name, err)
	}

	// wait for state to become active
	stateCh := lbp.waitForLBAction("deactivate", lb)
	_, ok = <-stateCh
	if !ok {
		return nil, fmt.Errorf("Timed out waiting for LB to activate [%s]. LB state: [%s]. LB status: [%s]", lb.Name, lb.State, lb.TransitioningMessage)
	}

	// wait for LB public endpoints
	lb, err = lbp.reloadLBService(lb)
	if err != nil {
		return nil, err
	}
	epChannel := lbp.waitForLBPublicEndpoints(1, lb)
	_, ok = <-epChannel
	if !ok {
		return nil, fmt.Errorf("Couldn't get publicEndpoints for LB [%s]", lb.Name)
	}

	return lbp.reloadLBService(lb)
}

func (lbp *LBProvider) reloadLBService(lb *client.LoadBalancerService) (*client.LoadBalancerService, error) {
	lb, err := lbp.client.LoadBalancerService.ById(lb.Id)
	if err != nil {
		return nil, fmt.Errorf("Couldn't reload LB [%s]. Error: %#v", lb.Name, err)
	}
	return lb, nil
}

func (lbp *LBProvider) getAllLBServices() ([]client.LoadBalancerService, error) {
	stack, err := lbp.getOrCreateSystemStack()
	if err != nil {
		return nil, err
	}
	opts := client.NewListOpts()
	opts.Filters["removed_null"] = "1"
	opts.Filters["environmentId"] = stack.Id
	lbs, err := lbp.client.LoadBalancerService.List(opts)
	if err != nil {
		return nil, fmt.Errorf("Coudln't get all lb services. Error: %#v", err)
	}

	return lbs.Data, nil
}

func (lbp *LBProvider) getLBServiceByName(name string) (*client.LoadBalancerService, error) {
	stack, err := lbp.getOrCreateSystemStack()
	if err != nil {
		return nil, err
	}

	opts := client.NewListOpts()
	opts.Filters["name"] = name
	opts.Filters["removed_null"] = "1"
	opts.Filters["environmentId"] = stack.Id
	lbs, err := lbp.client.LoadBalancerService.List(opts)
	if err != nil {
		return nil, fmt.Errorf("Coudln't get LB service by name [%s]. Error: %#v", name, err)
	}

	if len(lbs.Data) == 0 {
		return nil, nil
	}

	return &lbs.Data[0], nil
}

func (lbp *LBProvider) getKubernetesServiceByUUID(UUID string) (*client.KubernetesService, error) {
	opts := client.NewListOpts()
	opts.Filters["externalId"] = UUID
	opts.Filters["removed_null"] = "1"
	lbs, err := lbp.client.KubernetesService.List(opts)
	if err != nil {
		return nil, fmt.Errorf("Coudln't get service by uuid [%s]. Error: %#v", UUID, err)
	}

	if len(lbs.Data) == 0 {
		return nil, nil
	}
	return &lbs.Data[0], nil
}

func (lbp *LBProvider) waitForLBAction(action string, lb *client.LoadBalancerService) <-chan interface{} {
	cb := func(result chan<- interface{}) (bool, error) {
		lb, err := lbp.reloadLBService(lb)
		if err != nil {
			return false, err
		}
		if _, ok := lb.Actions[action]; ok {
			result <- lb
			return true, nil
		}
		return false, nil
	}
	return lbp.waitForCondition(action, cb)
}

func (lbp *LBProvider) waitForLBPublicEndpoints(count int, lb *client.LoadBalancerService) <-chan interface{} {
	cb := func(result chan<- interface{}) (bool, error) {
		lb, err := lbp.reloadLBService(lb)
		if err != nil {
			return false, err
		}
		if len(lb.PublicEndpoints) >= count {
			result <- lb
			return true, nil
		}
		return false, nil
	}
	return lbp.waitForCondition("publicEndpoints", cb)
}

func (lbp *LBProvider) waitForCondition(condition string, callback waitCallback) <-chan interface{} {
	ready := make(chan interface{}, 0)
	go func() {
		sleep := 2
		defer close(ready)
		for i := 0; i < 10; i++ {
			found, err := callback(ready)
			if err != nil {
				logrus.Errorf("Error: %#v", err)
				return
			}

			if found {
				return
			}
			time.Sleep(time.Second * time.Duration(sleep))
		}
		logrus.Errorf("Timed out waiting for condition [%s] ", condition)
	}()
	return ready
}
