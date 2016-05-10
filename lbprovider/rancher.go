package lbprovider

import (
	"encoding/json"
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/rancher/go-machine-service/locks"
	"github.com/rancher/go-rancher/client"
	"github.com/rancher/ingress-controller/lbconfig"
	utils "github.com/rancher/ingress-controller/utils"
	"os"
	"strconv"
	"strings"
	"time"
)

type PublicEndpoint struct {
	IPAddress string
	Port      int
}

const (
	controllerStackName        string = "kubernetes-ingress-controllers"
	controllerExternalIDPrefix string = "kubernetes-ingress-controllers://"
)

type RancherLBProvider struct {
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

	lbp := &RancherLBProvider{
		client: client,
		opts:   opts,
		stopCh: make(chan struct{}),
	}

	RegisterProvider(lbp.GetName(), lbp)
}

func (lbp *RancherLBProvider) ApplyConfig(lbConfig *lbconfig.LoadBalancerConfig) error {
	unlocker := locks.Lock(lbConfig.Name)
	if unlocker == nil {
		logrus.Infof("LB [%s] locked. Dropping event", lbConfig.Name)
		return nil
	}
	defer unlocker.Unlock()

	lb, err := lbp.createLBService(lbp.formatLBName(lbConfig.Name))
	if err != nil {
		return err
	}
	logrus.Infof("Setting service links for service [%s]", lb.Name)
	return lbp.setServiceLinks(lb, lbConfig)
}

func (lbp *RancherLBProvider) CleanupConfig(name string) error {
	unlocker := locks.Lock(name)
	if unlocker == nil {
		logrus.Infof("LB [%s] locked. Dropping event", name)
		return nil
	}
	defer unlocker.Unlock()
	fmtName := lbp.formatLBName(name)
	logrus.Infof("Deleting lb service [%s]", fmtName)

	return lbp.deleteLBService(fmtName)
}

func (lbp *RancherLBProvider) Stop() error {
	close(lbp.stopCh)
	logrus.Infof("shutting down syncEndpointsQueue")
	lbp.syncEndpointsQueue.Shutdown()
	logrus.Infof("Deleting ingress controller stack [%s]", controllerStackName)
	return lbp.deleteLBStack()
}

func (lbp *RancherLBProvider) Run(syncEndpointsQueue *utils.TaskQueue) {
	lbp.syncEndpointsQueue = syncEndpointsQueue
	go lbp.syncEndpointsQueue.Run(time.Second, lbp.stopCh)

	go lbp.syncupEndpoints()

	<-lbp.stopCh
	logrus.Infof("shutting down kubernetes-ingress-controller")
}

func (lbp *RancherLBProvider) syncupEndpoints() error {
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
			splitted := strings.SplitN(lb.Name, "-", 2)
			lbp.syncEndpointsQueue.Enqueue(fmt.Sprintf("%v/%v", splitted[0], splitted[1]))
		}
	}
}

func (lbp *RancherLBProvider) deleteLBStack() error {
	stack, err := lbp.getStack(controllerStackName)
	if err != nil {
		return err
	}
	if stack == nil {
		logrus.Infof("Ingress controller stack [%s] doesn't exist, no need to cleanup", controllerStackName)
	}
	_, err = lbp.client.Environment.ActionRemove(stack)
	return err
}

func (lbp *RancherLBProvider) deleteLBService(name string) error {
	stack, err := lbp.getStack(controllerStackName)
	if err != nil {
		return err
	}
	if stack == nil {
		logrus.Infof("Ingress controller stack [%s] doesn't exist, no need to cleanup LB ", controllerStackName)
	}
	lb, err := lbp.getLBServiceByName(name)
	if err != nil {
		return err
	}
	if lb == nil {
		logrus.Infof("LB [%s] doesn't exist, no need to cleanup ", name)
		return nil
	}
	_, err = lbp.client.LoadBalancerService.ActionRemove(lb)
	return err
}

func (lbp *RancherLBProvider) formatLBName(name string) string {
	return strings.Replace(name, "/", "-", -1)
}

func (lbp *RancherLBProvider) GetName() string {
	return "rancher"
}

func (lbp *RancherLBProvider) GetPublicEndpoints(configName string) []string {
	epStr := []string{}
	lbFmt := lbp.formatLBName(configName)
	lb, err := lbp.getLBServiceByName(lbFmt)
	if err != nil {
		logrus.Errorf("Failed to find LB [%s]: %v", lbFmt, err)
		return epStr
	}
	if lb == nil {
		logrus.Errorf("Failed to find LB [%s]", lbFmt)
		return epStr
	}

	epChannel := lbp.waitForLBPublicEndpoints(1, lb)
	_, ok := <-epChannel
	if !ok {
		logrus.Errorf("Couldn't get publicEndpoints for LB [%s]", lbFmt)
		return epStr
	}

	lb, err = lbp.reloadLBService(lb)
	if err != nil {
		logrus.Errorf("Failed to reload LB [%s]", lbFmt)
		return epStr
	}

	eps := lb.PublicEndpoints
	if len(eps) == 0 {
		logrus.Errorf("No public endpoints found for lb %s", lbFmt)
		return epStr
	}

	for _, epObj := range eps {
		ep := PublicEndpoint{}

		err = convertObject(epObj, &ep)
		if err != nil {
			logrus.Errorf("No public endpoints found for lb %v", err)
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

func (lbp *RancherLBProvider) getOrCreateSystemStack() (*client.Environment, error) {
	opts := client.NewListOpts()
	opts.Filters["name"] = controllerStackName
	opts.Filters["removed_null"] = "1"
	opts.Filters["external_id"] = controllerExternalIDPrefix

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

func (lbp *RancherLBProvider) getStack(name string) (*client.Environment, error) {
	opts := client.NewListOpts()
	opts.Filters["name"] = name
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

func (lbp *RancherLBProvider) createLBService(name string) (*client.LoadBalancerService, error) {
	stack, err := lbp.getOrCreateSystemStack()
	if err != nil {
		return nil, err
	}
	lb, err := lbp.getLBServiceByName(name)
	if err != nil {
		return nil, err
	}
	if lb != nil {
		if lb.State != "active" {
			return lbp.activateLBService(lb)
		}
		return lb, nil
	}

	// private port 80 will be overritten by ports
	// in hostname routing rules
	lbPorts := []string{"80:80"}

	lb = &client.LoadBalancerService{
		Name:          name,
		EnvironmentId: stack.Id,
		LaunchConfig: &client.LaunchConfig{
			Ports: lbPorts,
		},
		ExternalId: fmt.Sprintf("%v%v", controllerExternalIDPrefix, name),
	}

	lb, err = lbp.client.LoadBalancerService.Create(lb)
	if err != nil {
		return nil, fmt.Errorf("Unable to create LB [%s]. Error: %#v", name, err)
	}

	return lbp.activateLBService(lb)
}

func (lbp *RancherLBProvider) setServiceLinks(lb *client.LoadBalancerService, lbConfig *lbconfig.LoadBalancerConfig) error {
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
		svc, err := lbp.getKubernetesServiceByName(bcknd.Name, bcknd.Namespace)
		if err != nil {
			return err
		}
		if svc == nil {
			return fmt.Errorf("Failed to find service [%s] in stack [%s] in Rancher", bcknd.Name, bcknd.Namespace)
		}
		ports := []string{}
		var port string
		bckndPort := strconv.Itoa(bcknd.Port)
		if bcknd.Host != "" && bcknd.Path != "" {
			// public port is always 80
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

func (lbp *RancherLBProvider) activateLBService(lb *client.LoadBalancerService) (*client.LoadBalancerService, error) {
	// activate LB
	actionChannel := lbp.waitForLBAction("activate", lb)
	_, ok := <-actionChannel
	if !ok {
		return nil, fmt.Errorf("Couldn't call activate on LB [%s]", lb.Name)
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
		return nil, fmt.Errorf("Timed out waiting for LB to activate [%s]. Transitioning state: [%v]", lb.Name, lb.TransitioningMessage)
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

func (lbp *RancherLBProvider) reloadLBService(lb *client.LoadBalancerService) (*client.LoadBalancerService, error) {
	lb, err := lbp.client.LoadBalancerService.ById(lb.Id)
	if err != nil {
		return nil, fmt.Errorf("Couldn't reload LB [%s]. Error: %#v", lb.Name, err)
	}
	return lb, nil
}

func (lbp *RancherLBProvider) getAllLBServices() ([]client.LoadBalancerService, error) {
	stack, err := lbp.getOrCreateSystemStack()
	if err != nil {
		return nil, err
	}
	opts := client.NewListOpts()
	opts.Filters["removed_null"] = "1"
	opts.Filters["environment_id"] = stack.Id
	lbs, err := lbp.client.LoadBalancerService.List(opts)
	if err != nil {
		return nil, fmt.Errorf("Coudln't get all lb services. Error: %#v", err)
	}

	return lbs.Data, nil
}

func (lbp *RancherLBProvider) getLBServiceByName(name string) (*client.LoadBalancerService, error) {
	stack, err := lbp.getOrCreateSystemStack()
	if err != nil {
		return nil, err
	}

	opts := client.NewListOpts()
	opts.Filters["name"] = name
	opts.Filters["removed_null"] = "1"
	opts.Filters["environment_id"] = stack.Id
	lbs, err := lbp.client.LoadBalancerService.List(opts)
	if err != nil {
		return nil, fmt.Errorf("Coudln't get LB service by name [%s]. Error: %#v", name, err)
	}

	if len(lbs.Data) == 0 {
		return nil, nil
	}

	return &lbs.Data[0], nil
}

func (lbp *RancherLBProvider) getKubernetesServiceByName(name string, stackName string) (*client.KubernetesService, error) {
	stack, err := lbp.getStack(stackName)
	if err != nil {
		return nil, err
	}

	if stack == nil {
		return nil, fmt.Errorf("Coudln't get stack by name [%s]", stackName)
	}

	opts := client.NewListOpts()
	opts.Filters["name"] = name
	opts.Filters["removed_null"] = "1"
	opts.Filters["environment_id"] = stack.Id
	lbs, err := lbp.client.KubernetesService.List(opts)
	if err != nil {
		return nil, fmt.Errorf("Coudln't get service by name [%s]. Error: %#v", name, err)
	}

	if len(lbs.Data) == 0 {
		return nil, nil
	}

	return &lbs.Data[0], nil
}

func (lbp *RancherLBProvider) waitForLBAction(action string, lb *client.LoadBalancerService) <-chan interface{} {
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

func (lbp *RancherLBProvider) waitForLBPublicEndpoints(count int, lb *client.LoadBalancerService) <-chan interface{} {
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

func (lbp *RancherLBProvider) waitForCondition(condition string, callback waitCallback) <-chan interface{} {
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
