package lbprovider

import (
	"encoding/json"
	"fmt"
	"github.com/golang/glog"
	"github.com/rancher/go-rancher/client"
	"github.com/rancher/rancher-ingress/lbconfig"
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
	lbNameFormat      string = "lb-%s"
	lbStackName       string = "kubernetes-ingress-loadbalancers"
	lbStackExternalID string = "kubernetes-ingress-loadbalancers://"
)

func init() {
	cattleURL := os.Getenv("CATTLE_URL")
	if len(cattleURL) == 0 {
		glog.Info("CATTLE_URL is not set, skipping init of Rancher LB provider")
		return
	}

	cattleAccessKey := os.Getenv("CATTLE_ACCESS_KEY")
	if len(cattleAccessKey) == 0 {
		glog.Info("CATTLE_ACCESS_KEY is not set, skipping init of Rancher LB provider")
		return
	}

	cattleSecretKey := os.Getenv("CATTLE_SECRET_KEY")
	if len(cattleSecretKey) == 0 {
		glog.Info("CATTLE_SECRET_KEY is not set, skipping init of Rancher LB provider")
		return
	}

	client, err := client.NewRancherClient(&client.ClientOpts{
		Url:       cattleURL,
		AccessKey: cattleAccessKey,
		SecretKey: cattleSecretKey,
	})

	if err != nil {
		glog.Fatalf("Failed to create Rancher client %v", err)
	}

	lbp := &RancherLBProvider{
		client: client,
	}

	RegisterProvider(lbp.GetName(), lbp)
}

type RancherLBProvider struct {
	client *client.RancherClient
}

func (lbc *RancherLBProvider) ApplyConfig(lbConfig *lbconfig.LoadBalancerConfig) error {
	lb, err := lbc.createLBService(lbc.formatLBName(lbConfig.Name))
	if err != nil {
		return err
	}
	glog.Infof("Setting service links for service [%s]", lb.Name)
	return lbc.setServiceLinks(lb, lbConfig)
}

func (lbc *RancherLBProvider) CleanupConfig(name string) error {
	fmtName := lbc.formatLBName(name)
	glog.Infof("Deleting lb service [%s]", fmtName)

	return lbc.deleteLBService(fmtName)
}

func (lbc *RancherLBProvider) Stop() error {
	glog.Infof("Deleting lb stack [%s]", lbStackName)

	return lbc.deleteLBStack()
}

func (lbc *RancherLBProvider) deleteLBStack() error {
	stack, err := lbc.getStack(lbStackName)
	if err != nil {
		return err
	}
	if stack == nil {
		glog.Infof("System LB stack [%s] doesn't exist, no need to cleanup", lbStackName)
	}
	_, err = lbc.client.Environment.ActionRemove(stack)
	return err
}

func (lbc *RancherLBProvider) deleteLBService(name string) error {
	stack, err := lbc.getStack(lbStackName)
	if err != nil {
		return err
	}
	if stack == nil {
		glog.Infof("System LB stack [%s] doesn't exist, no need to cleanup LB ", lbStackName)
	}
	lb, err := lbc.getLBServiceByName(name)
	if err != nil {
		return err
	}
	_, err = lbc.client.LoadBalancerService.ActionRemove(lb)
	return err
}

func (lbc *RancherLBProvider) formatLBName(name string) string {
	return strings.Replace(name, "/", "-", -1)
}

func (lbc *RancherLBProvider) GetName() string {
	return "rancher"
}

func (lbc *RancherLBProvider) GetPublicEndpoint(configName string) string {
	epStr := ""
	lbFmt := lbc.formatLBName(configName)
	lb, err := lbc.createLBService(lbFmt)
	if err != nil {
		glog.Errorf("Failed to find lb service [%s] %v", lbFmt, err)
		return epStr
	}
	eps := lb.PublicEndpoints
	if len(eps) == 0 {
		glog.Errorf("No public endpoints found for lb %s", lbFmt)
		return epStr
	}

	ep := PublicEndpoint{}

	err = convertObject(eps[0], &ep)
	if err != nil {
		glog.Errorf("No public endpoints found for lb %v", err)
		return epStr
	}

	return ep.IPAddress
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

func (lbc *RancherLBProvider) getOrCreateSystemStack() (*client.Environment, error) {
	opts := client.NewListOpts()
	opts.Filters["name"] = lbStackName
	opts.Filters["removed_null"] = "1"
	opts.Filters["external_id"] = lbStackExternalID

	envs, err := lbc.client.Environment.List(opts)
	if err != nil {
		return nil, fmt.Errorf("Coudln't get stack by name [%s]. Error: %#v", lbStackName, err)
	}

	if len(envs.Data) >= 1 {
		return &envs.Data[0], nil
	}

	env := &client.Environment{
		Name:       lbStackName,
		ExternalId: lbStackExternalID,
	}

	env, err = lbc.client.Environment.Create(env)
	if err != nil {
		return nil, fmt.Errorf("Couldn't create stack [%s] for kubernetes LB ingress. Error: %#v", lbStackName, err)
	}
	return env, nil
}

func (lbc *RancherLBProvider) getStack(name string) (*client.Environment, error) {
	opts := client.NewListOpts()
	opts.Filters["name"] = name
	opts.Filters["removed_null"] = "1"

	envs, err := lbc.client.Environment.List(opts)
	if err != nil {
		return nil, fmt.Errorf("Coudln't get stack by name [%s]. Error: %#v", name, err)
	}

	if len(envs.Data) >= 1 {
		return &envs.Data[0], nil
	}
	return nil, nil
}

func (lbc *RancherLBProvider) createLBService(name string) (*client.LoadBalancerService, error) {
	stack, err := lbc.getOrCreateSystemStack()
	if err != nil {
		return nil, err
	}
	//FIXME - check if public endpoint got changed
	//this is the only time ingress should be updated
	lb, err := lbc.getLBServiceByName(name)
	if err != nil {
		return nil, err
	}
	if lb != nil {
		if lb.State != "active" {
			return lbc.activateLBService(lb)
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
	}

	lb, err = lbc.client.LoadBalancerService.Create(lb)
	if err != nil {
		return nil, fmt.Errorf("Unable to create LB [%s]. Error: %#v", name, err)
	}

	return lbc.activateLBService(lb)
}

func (lbc *RancherLBProvider) setServiceLinks(lb *client.LoadBalancerService, lbConfig *lbconfig.LoadBalancerConfig) error {
	if len(lbConfig.FrontendServices) == 0 {
		glog.Infof("Config [%s] doesn't have any rules defined", lbConfig.Name)
		return nil
	}
	actionChannel := lbc.waitForLBAction("setservicelinks", lb)
	_, ok := <-actionChannel
	if !ok {
		return fmt.Errorf("Couldn't call setservicelinks on LB [%s]", lb.Name)
	}

	lb, err := lbc.reloadLBService(lb)
	if err != nil {
		return err
	}
	serviceLinks := &client.SetLoadBalancerServiceLinksInput{}

	for _, bcknd := range lbConfig.FrontendServices[0].BackendServices {
		svc, err := lbc.getKubernetesServiceByName(bcknd.Name, lbConfig.Namespace)
		if err != nil {
			return err
		}
		if svc == nil {
			return fmt.Errorf("Failed to find service [%s] in stack [%s] in Rancher", bcknd.Name, lbConfig.Namespace)
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

	_, err = lbc.client.LoadBalancerService.ActionSetservicelinks(lb, serviceLinks)
	if err != nil {
		return fmt.Errorf("Failed to set service links for lb [%s]. Error: %#v", lb.Name, err)
	}
	return nil
}

func (lbc *RancherLBProvider) activateLBService(lb *client.LoadBalancerService) (*client.LoadBalancerService, error) {
	// activate LB
	actionChannel := lbc.waitForLBAction("activate", lb)
	_, ok := <-actionChannel
	if !ok {
		return nil, fmt.Errorf("Couldn't call activate on LB [%s]", lb.Name)
	}
	lb, err := lbc.reloadLBService(lb)
	if err != nil {
		return nil, err
	}
	_, err = lbc.client.LoadBalancerService.ActionActivate(lb)
	if err != nil {
		return nil, fmt.Errorf("Error creating LB [%s]. Couldn't activate LB. Error: %#v", lb.Name, err)
	}

	// wait for state to become active
	stateCh := lbc.waitForLBAction("deactivate", lb)
	_, ok = <-stateCh
	if !ok {
		return nil, fmt.Errorf("Timed out waiting for LB to activate %s", lb.Name)
	}

	// wait for LB public endpoints
	lb, err = lbc.reloadLBService(lb)
	if err != nil {
		return nil, err
	}
	epChannel := lbc.waitForLBPublicEndpoints(1, lb)
	_, ok = <-epChannel
	if !ok {
		return nil, fmt.Errorf("Couldn't get publicEndpoints for LB [%s]", lb.Name)
	}

	return lbc.reloadLBService(lb)
}

func (lbc *RancherLBProvider) reloadLBService(lb *client.LoadBalancerService) (*client.LoadBalancerService, error) {
	lb, err := lbc.client.LoadBalancerService.ById(lb.Id)
	if err != nil {
		return nil, fmt.Errorf("Couldn't reload LB [%s]. Error: %#v", lb.Name, err)
	}
	return lb, nil
}

func (lbc *RancherLBProvider) getLBServiceByName(name string) (*client.LoadBalancerService, error) {
	stack, err := lbc.getOrCreateSystemStack()
	if err != nil {
		return nil, err
	}

	opts := client.NewListOpts()
	opts.Filters["name"] = name
	opts.Filters["removed_null"] = "1"
	opts.Filters["environment_id"] = stack.Id
	lbs, err := lbc.client.LoadBalancerService.List(opts)
	if err != nil {
		return nil, fmt.Errorf("Coudln't get LB service by name [%s]. Error: %#v", name, err)
	}

	if len(lbs.Data) == 0 {
		return nil, nil
	}

	return &lbs.Data[0], nil
}

func (lbc *RancherLBProvider) getKubernetesServiceByName(name string, stackName string) (*client.KubernetesService, error) {
	stack, err := lbc.getStack(stackName)
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
	lbs, err := lbc.client.KubernetesService.List(opts)
	if err != nil {
		return nil, fmt.Errorf("Coudln't get service by name [%s]. Error: %#v", name, err)
	}

	if len(lbs.Data) == 0 {
		return nil, nil
	}

	return &lbs.Data[0], nil
}

func (lbc *RancherLBProvider) waitForLBAction(action string, lb *client.LoadBalancerService) <-chan interface{} {
	cb := func(result chan<- interface{}) (bool, error) {
		lb, err := lbc.reloadLBService(lb)
		if err != nil {
			return false, err
		}
		if _, ok := lb.Actions[action]; ok {
			result <- lb
			return true, nil
		}
		return false, nil
	}
	return lbc.waitForCondition(action, cb)
}

func (lbc *RancherLBProvider) waitForLBPublicEndpoints(count int, lb *client.LoadBalancerService) <-chan interface{} {
	cb := func(result chan<- interface{}) (bool, error) {
		lb, err := lbc.reloadLBService(lb)
		if err != nil {
			return false, err
		}
		if len(lb.PublicEndpoints) == count {
			result <- lb
			return true, nil
		}
		return false, nil
	}
	return lbc.waitForCondition("publicEndpoints", cb)
}

func (lbc *RancherLBProvider) waitForCondition(condition string, callback waitCallback) <-chan interface{} {
	ready := make(chan interface{}, 0)
	go func() {
		sleep := 2
		defer close(ready)
		for i := 0; i < 5; i++ {
			found, err := callback(ready)
			if err != nil {
				glog.Errorf("Error: %#v", err)
				return
			}

			if found {
				return
			}
			time.Sleep(time.Second * time.Duration(sleep))
		}
		glog.Errorf("Timed out waiting for condition %s.", condition)
	}()
	return ready
}
