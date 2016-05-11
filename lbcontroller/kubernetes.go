package lbcontroller

import (
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/rancher/ingress-controller/lbconfig"
	"github.com/rancher/ingress-controller/lbprovider"
	utils "github.com/rancher/ingress-controller/utils"
	"github.com/spf13/pflag"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/unversioned"
	"k8s.io/kubernetes/pkg/apis/extensions"
	"k8s.io/kubernetes/pkg/client/cache"
	"k8s.io/kubernetes/pkg/client/record"
	"k8s.io/kubernetes/pkg/client/restclient"
	client "k8s.io/kubernetes/pkg/client/unversioned"
	"k8s.io/kubernetes/pkg/controller/framework"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/util/intstr"
	"k8s.io/kubernetes/pkg/watch"
	"os"
)

var (
	flags        = pflag.NewFlagSet("", pflag.ExitOnError)
	resyncPeriod = flags.Duration("sync-period", 30*time.Second,
		`Relist and confirm cloud resources this often.`)
)

func init() {
	var server string
	if server = os.Getenv("KUBERNETES_URL"); len(server) == 0 {
		logrus.Info("KUBERNETES_URL is not set, skipping init of kubernetes controller")
		return
	}
	config := &restclient.Config{
		Host:          server,
		ContentConfig: restclient.ContentConfig{GroupVersion: &unversioned.GroupVersion{Version: "v1"}},
	}
	kubeClient, err := client.New(config)

	if err != nil {
		logrus.Fatalf("failed to create kubernetes client: %v", err)
	}

	lbc, err := newLoadBalancerController(kubeClient, *resyncPeriod, api.NamespaceAll)
	if err != nil {
		logrus.Fatalf("%v", err)
	}

	RegisterController(lbc.GetName(), lbc)
}

type loadBalancerController struct {
	client         *client.Client
	ingController  *framework.Controller
	endpController *framework.Controller
	svcController  *framework.Controller
	ingLister      utils.StoreToIngressLister
	svcLister      cache.StoreToServiceLister
	endpLister     cache.StoreToEndpointsLister
	recorder       record.EventRecorder
	syncQueue      *utils.TaskQueue
	ingQueue       *utils.TaskQueue
	cleanupQueue   *utils.TaskQueue
	stopLock       sync.Mutex
	shutdown       bool
	stopCh         chan struct{}
	lbProvider     lbprovider.LBProvider
}

func newLoadBalancerController(kubeClient *client.Client, resyncPeriod time.Duration, namespace string) (*loadBalancerController, error) {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(logrus.Infof)
	eventBroadcaster.StartRecordingToSink(kubeClient.Events(""))
	lbc := loadBalancerController{
		client:   kubeClient,
		stopCh:   make(chan struct{}),
		recorder: eventBroadcaster.NewRecorder(api.EventSource{Component: "loadbalancer-controller"}),
	}

	lbc.syncQueue = utils.NewTaskQueue(lbc.sync)
	lbc.ingQueue = utils.NewTaskQueue(lbc.updateIngressStatus)
	lbc.cleanupQueue = utils.NewTaskQueue(lbc.cleanupLB)

	ingEventHandler := framework.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			addIng := obj.(*extensions.Ingress)
			lbc.recorder.Eventf(addIng, api.EventTypeNormal, "CREATE", fmt.Sprintf("%s/%s", addIng.Namespace, addIng.Name))
			lbc.ingQueue.Enqueue(obj)
			lbc.syncQueue.Enqueue(obj)
		},
		DeleteFunc: func(obj interface{}) {
			upIng := obj.(*extensions.Ingress)
			lbc.recorder.Eventf(upIng, api.EventTypeNormal, "DELETE", fmt.Sprintf("%s/%s", upIng.Namespace, upIng.Name))
			lbc.syncQueue.Enqueue(obj)
			lbc.cleanupQueue.Enqueue(obj)
		},
		UpdateFunc: func(old, cur interface{}) {
			if !reflect.DeepEqual(old, cur) {
				upIng := cur.(*extensions.Ingress)
				lbc.recorder.Eventf(upIng, api.EventTypeNormal, "UPDATE", fmt.Sprintf("%s/%s", upIng.Namespace, upIng.Name))
				lbc.ingQueue.Enqueue(cur)
				lbc.syncQueue.Enqueue(cur)
			}
		},
	}

	eventHandler := framework.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			lbc.syncQueue.Enqueue(obj)
		},
		DeleteFunc: func(obj interface{}) {
			lbc.syncQueue.Enqueue(obj)
		},
		UpdateFunc: func(old, cur interface{}) {
			if !reflect.DeepEqual(old, cur) {
				lbc.syncQueue.Enqueue(cur)
			}
		},
	}

	lbc.ingLister.Store, lbc.ingController = framework.NewInformer(
		&cache.ListWatch{
			ListFunc:  ingressListFunc(lbc.client, namespace),
			WatchFunc: ingressWatchFunc(lbc.client, namespace),
		},
		&extensions.Ingress{}, resyncPeriod, ingEventHandler)

	lbc.endpLister.Store, lbc.endpController = framework.NewInformer(
		&cache.ListWatch{
			ListFunc:  endpointsListFunc(lbc.client, namespace),
			WatchFunc: endpointsWatchFunc(lbc.client, namespace),
		},
		&api.Endpoints{}, resyncPeriod, eventHandler)

	lbc.svcLister.Store, lbc.svcController = framework.NewInformer(
		&cache.ListWatch{
			ListFunc:  serviceListFunc(lbc.client, namespace),
			WatchFunc: serviceWatchFunc(lbc.client, namespace),
		},
		&api.Service{}, resyncPeriod, framework.ResourceEventHandlerFuncs{})

	return &lbc, nil
}

func (lbc *loadBalancerController) cleanupLB(key string) {
	if err := lbc.lbProvider.CleanupConfig(key); err != nil {
		lbc.syncQueue.Requeue(key, fmt.Errorf("Failed to cleanup lb [%s]", key))
		return
	}
}

func ingressListFunc(c *client.Client, ns string) func(api.ListOptions) (runtime.Object, error) {
	return func(opts api.ListOptions) (runtime.Object, error) {
		return c.Extensions().Ingress(ns).List(opts)
	}
}

func ingressWatchFunc(c *client.Client, ns string) func(options api.ListOptions) (watch.Interface, error) {
	return func(options api.ListOptions) (watch.Interface, error) {
		return c.Extensions().Ingress(ns).Watch(options)
	}
}

func serviceListFunc(c *client.Client, ns string) func(api.ListOptions) (runtime.Object, error) {
	return func(opts api.ListOptions) (runtime.Object, error) {
		return c.Services(ns).List(opts)
	}
}

func serviceWatchFunc(c *client.Client, ns string) func(options api.ListOptions) (watch.Interface, error) {
	return func(options api.ListOptions) (watch.Interface, error) {
		return c.Services(ns).Watch(options)
	}
}

func endpointsListFunc(c *client.Client, ns string) func(api.ListOptions) (runtime.Object, error) {
	return func(opts api.ListOptions) (runtime.Object, error) {
		return c.Endpoints(ns).List(opts)
	}
}

func endpointsWatchFunc(c *client.Client, ns string) func(options api.ListOptions) (watch.Interface, error) {
	return func(options api.ListOptions) (watch.Interface, error) {
		return c.Endpoints(ns).Watch(options)
	}
}

func (lbc *loadBalancerController) controllersInSync() bool {
	return lbc.ingController.HasSynced() && lbc.svcController.HasSynced() && lbc.endpController.HasSynced()
}

func (lbc *loadBalancerController) sync(key string) {
	if !lbc.controllersInSync() {
		lbc.syncQueue.Requeue(key, fmt.Errorf("deferring sync till endpoints controller has synced"))
		return
	}
	for _, cfg := range lbc.GetLBConfigs() {
		if err := lbc.lbProvider.ApplyConfig(cfg); err != nil {
			logrus.Errorf("Failed to apply lb config on provider: %v", err)
		}
	}
}

func (lbc *loadBalancerController) updateIngressStatus(key string) {
	if !lbc.controllersInSync() {
		lbc.ingQueue.Requeue(key, fmt.Errorf("deferring sync till endpoints controller has synced"))
		return
	}

	obj, ingExists, err := lbc.ingLister.Store.GetByKey(key)
	if err != nil {
		lbc.ingQueue.Requeue(key, err)
		return
	}

	if !ingExists {
		return
	}

	ing := obj.(*extensions.Ingress)

	ingClient := lbc.client.Extensions().Ingress(ing.Namespace)

	currIng, err := ingClient.Get(ing.Name)
	if err != nil {
		logrus.Errorf("unexpected error searching Ingress %v/%v: %v", ing.Namespace, ing.Name, err)
		return
	}

	lbIPs := ing.Status.LoadBalancer.Ingress
	publicEndpoints := lbc.getPublicEndpoints(key)
	toAdd, toRemove := lbc.getIPsToAddRemove(lbIPs, publicEndpoints)

	// add missing
	for _, IP := range toAdd {
		logrus.Infof("Updating ingress %v/%v with IP %v", ing.Namespace, ing.Name, IP)
		currIng.Status.LoadBalancer.Ingress = append(currIng.Status.LoadBalancer.Ingress, api.LoadBalancerIngress{
			IP: IP,
		})
		if _, err := ingClient.UpdateStatus(currIng); err != nil {
			lbc.recorder.Eventf(currIng, api.EventTypeWarning, "UPDATE", "error: %v", err)
			return
		}

		lbc.recorder.Eventf(currIng, api.EventTypeNormal, "CREATE", "ip: %v", IP)
	}

	// remove extra ips
	for idx, lbStatus := range currIng.Status.LoadBalancer.Ingress {
		for _, IP := range toRemove {
			if IP == lbStatus.IP {
				logrus.Infof("Updating ingress %v/%v. Removing IP %v", ing.Namespace, ing.Name, lbStatus.IP)

				currIng.Status.LoadBalancer.Ingress = append(currIng.Status.LoadBalancer.Ingress[:idx],
					currIng.Status.LoadBalancer.Ingress[idx+1:]...)
				if _, err := ingClient.UpdateStatus(currIng); err != nil {
					lbc.recorder.Eventf(currIng, api.EventTypeWarning, "UPDATE", "error: %v", err)
					break
				}
				lbc.recorder.Eventf(currIng, api.EventTypeNormal, "DELETE", "ip: %v", lbStatus.IP)
				break
			}
		}
	}
}

func (lbc *loadBalancerController) getIPsToAddRemove(lbings []api.LoadBalancerIngress, IPs []string) ([]string, []string) {
	add := []string{}
	remove := []string{}
	//find entries to remove
	for _, lbing := range lbings {
		found := false
		for _, IP := range IPs {
			if lbing.IP == IP {
				found = true
				break
			}
		}
		if !found {
			remove = append(remove, lbing.IP)
		}
	}
	// find entries to add
	for _, IP := range IPs {
		found := false
		for _, lbing := range lbings {
			if lbing.IP == IP {
				found = true
			}
		}
		if !found {
			add = append(add, IP)
		}
	}
	return add, remove
}

func (lbc *loadBalancerController) isStatusIPDefined(lbings []api.LoadBalancerIngress, IP string) bool {
	for _, lbing := range lbings {
		if lbing.IP == IP {
			return true
		}
	}
	return false
}

func (lbc *loadBalancerController) getPublicEndpoints(key string) []string {
	providerEP := lbc.lbProvider.GetPublicEndpoints(key)
	return providerEP
}

// Starts a load balancer controller
func (lbc *loadBalancerController) Run(provider lbprovider.LBProvider) {
	logrus.Infof("starting kubernetes-ingress-controller")
	go lbc.ingController.Run(lbc.stopCh)
	go lbc.endpController.Run(lbc.stopCh)
	go lbc.svcController.Run(lbc.stopCh)

	go lbc.syncQueue.Run(time.Second, lbc.stopCh)
	go lbc.ingQueue.Run(time.Second, lbc.stopCh)
	go lbc.cleanupQueue.Run(time.Second, lbc.stopCh)

	lbc.lbProvider = provider
	go lbc.lbProvider.Run(utils.NewTaskQueue(lbc.updateIngressStatus))

	<-lbc.stopCh
	logrus.Infof("shutting down kubernetes-ingress-controller")
}

func (lbc *loadBalancerController) GetLBConfigs() []*lbconfig.LoadBalancerConfig {
	backends := []*lbconfig.BackendService{}
	ings := lbc.ingLister.Store.List()
	lbConfigs := []*lbconfig.LoadBalancerConfig{}
	if len(ings) == 0 {
		return lbConfigs
	}
	for _, ingIf := range ings {
		ing := ingIf.(*extensions.Ingress)
		// process default rule
		if ing.Spec.Backend != nil {
			svcName := ing.Spec.Backend.ServiceName
			svcPort := ing.Spec.Backend.ServicePort.IntValue()
			svc, _ := lbc.getService(svcName, ing.GetNamespace())
			if svc != nil {
				backend := lbc.getServiceBackend(svc, svcPort, "", "")
				if backend != nil {
					backends = append(backends, backend)
				}
			}
		}

		for _, rule := range ing.Spec.Rules {
			logrus.Infof("Processing ingress rule %v", rule)
			// process http rules only
			if rule.IngressRuleValue.HTTP == nil {
				continue
			}

			// process host name routing rules
			for _, path := range rule.HTTP.Paths {
				svcName := path.Backend.ServiceName
				svc, _ := lbc.getService(svcName, ing.GetNamespace())
				if svc == nil {
					continue
				}
				backend := lbc.getServiceBackend(svc, path.Backend.ServicePort.IntValue(), path.Path, rule.Host)
				if backend != nil {
					backends = append(backends, backend)
				}
			}
		}
		//FIXME - add second frontend service for https port
		frontEndServices := []*lbconfig.FrontendService{}
		frontEndService := &lbconfig.FrontendService{
			Name:            ing.Name,
			Port:            80,
			BackendServices: backends,
		}
		frontEndServices = append(frontEndServices, frontEndService)
		lbConfig := &lbconfig.LoadBalancerConfig{
			Name:             fmt.Sprintf("%v/%v", ing.GetNamespace(), ing.Name),
			FrontendServices: frontEndServices,
		}
		lbConfigs = append(lbConfigs, lbConfig)
	}

	return lbConfigs
}

func (lbc *loadBalancerController) getServiceBackend(svc *api.Service, port int, path string, host string) *lbconfig.BackendService {
	var backend *lbconfig.BackendService
	for _, servicePort := range svc.Spec.Ports {
		if servicePort.Port == port {
			eps := lbc.getEndpoints(svc, servicePort.TargetPort, api.ProtocolTCP)
			if len(eps) == 0 {
				continue
			}
			backend = &lbconfig.BackendService{
				Name:      svc.Name,
				Namespace: svc.Namespace,
				Endpoints: eps,
				Algorithm: "roundrobin",
				Path:      path,
				Host:      host,
				Port:      eps[0].Port,
			}
			break
		}
	}
	return backend
}

func (lbc *loadBalancerController) getService(svcName string, namespace string) (*api.Service, error) {
	svcKey := fmt.Sprintf("%v/%v", namespace, svcName)
	svcObj, svcExists, err := lbc.svcLister.Store.GetByKey(svcKey)
	if err != nil {
		logrus.Infof("error getting service [%s] from the cache: %v", svcKey, err)
		return nil, err
	}

	if !svcExists {
		logrus.Warningf("service [%s] does no exists", svcKey)
		return nil, nil
	}

	svc := svcObj.(*api.Service)
	return svc, nil
}

// getEndpoints returns a list of <endpoint ip> for a given service combination.
func (lbc *loadBalancerController) getEndpoints(s *api.Service, servicePort intstr.IntOrString, proto api.Protocol) []lbconfig.Endpoint {
	ep, err := lbc.endpLister.GetServiceEndpoints(s)
	if err != nil {
		logrus.Warningf("unexpected error getting service endpoints: %v", err)
		return []lbconfig.Endpoint{}
	}
	lbEndpoints := []lbconfig.Endpoint{}
	for _, ss := range ep.Subsets {
		for _, epPort := range ss.Ports {

			if !reflect.DeepEqual(epPort.Protocol, proto) {
				continue
			}

			var targetPort int
			switch servicePort.Type {
			case intstr.Int:
				if epPort.Port == servicePort.IntValue() {
					targetPort = epPort.Port
				}
			case intstr.String:
				if epPort.Name == servicePort.StrVal {
					targetPort = epPort.Port
				}
			}

			if targetPort == 0 {
				continue
			}

			for _, epAddress := range ss.Addresses {
				lbEndpoint := lbconfig.Endpoint{
					IP:   epAddress.IP,
					Port: targetPort,
				}
				lbEndpoints = append(lbEndpoints, lbEndpoint)
			}
		}
	}

	return lbEndpoints
}

// Stop stops the loadbalancer controller.
func (lbc *loadBalancerController) Stop() error {
	lbc.stopLock.Lock()
	defer lbc.stopLock.Unlock()

	if !lbc.shutdown {
		//stop the provider
		if err := lbc.lbProvider.Stop(); err != nil {
			return err
		}
		lbc.removeFromIngress()
		close(lbc.stopCh)
		logrus.Infof("shutting down controller queues")
		lbc.shutdown = true
		lbc.syncQueue.Shutdown()
		lbc.ingQueue.Shutdown()
		lbc.cleanupQueue.Shutdown()

		return nil
	}

	return fmt.Errorf("shutdown already in progress")
}

func (lbc *loadBalancerController) removeFromIngress() {
	ings := lbc.ingLister.Store.List()
	logrus.Infof("updating %v Ingress rule/s", len(ings))
	for _, cur := range ings {
		ing := cur.(*extensions.Ingress)

		ingClient := lbc.client.Extensions().Ingress(ing.Namespace)
		currIng, err := ingClient.Get(ing.Name)
		if err != nil {
			logrus.Errorf("unexpected error searching Ingress %v/%v: %v", ing.Namespace, ing.Name, err)
			continue
		}

		for idx, lbStatus := range currIng.Status.LoadBalancer.Ingress {
			logrus.Infof("Updating ingress %v/%v. Removing IP %v", ing.Namespace, ing.Name, lbStatus.IP)

			currIng.Status.LoadBalancer.Ingress = append(currIng.Status.LoadBalancer.Ingress[:idx],
				currIng.Status.LoadBalancer.Ingress[idx+1:]...)
			if _, err := ingClient.UpdateStatus(currIng); err != nil {
				lbc.recorder.Eventf(currIng, api.EventTypeWarning, "UPDATE", "error: %v", err)
				continue
			}
			lbc.recorder.Eventf(currIng, api.EventTypeNormal, "DELETE", "ip: %v", lbStatus.IP)
		}
	}
}

func (lbc *loadBalancerController) GetName() string {
	return "kubernetes"
}

func (lbc *loadBalancerController) IsHealthy() bool {
	_, err := lbc.client.Extensions().Ingress(api.NamespaceAll).List(api.ListOptions{})
	if err != nil {
		logrus.Errorf("Health check failed: unable to reach Kubernetes. Error: %#v", err)
		return false
	}
	return true
}
