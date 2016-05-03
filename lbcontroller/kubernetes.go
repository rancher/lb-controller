package lbcontroller

import (
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/golang/glog"

	"github.com/rancher/rancher-ingress/utils"
	"github.com/spf13/pflag"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/errors"
	"k8s.io/kubernetes/pkg/api/unversioned"
	"k8s.io/kubernetes/pkg/apis/extensions"
	"k8s.io/kubernetes/pkg/client/cache"
	"k8s.io/kubernetes/pkg/client/record"
	"k8s.io/kubernetes/pkg/client/restclient"
	client "k8s.io/kubernetes/pkg/client/unversioned"
	"k8s.io/kubernetes/pkg/controller/framework"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/util/wait"
	"k8s.io/kubernetes/pkg/watch"
	"os"
)

var (
	flags        = pflag.NewFlagSet("", pflag.ExitOnError)
	resyncPeriod = flags.Duration("sync-period", 30*time.Second,
		`Relist and confirm cloud resources this often.`)

	watchNamespace = flags.String("watch-namespace", api.NamespaceAll,
		`Namespace to watch for Ingress. Default is to watch all namespaces`)
)

func init() {
	var server string
	if server = os.Getenv("KUBERNETES_SERVER"); len(server) == 0 {
		glog.Info("KUBERNETES_SERVER is not set, skipping init of kubernetes controller")
		return
	}
	config := &restclient.Config{
		Host:          server,
		ContentConfig: restclient.ContentConfig{GroupVersion: &unversioned.GroupVersion{Version: "v1"}},
	}
	kubeClient, err := client.New(config)

	if err != nil {
		glog.Fatalf("failed to create kubernetes client: %v", err)
	}

	runtimePodInfo, err := getPodDetails(kubeClient)
	if err != nil {
		glog.Errorf("Failed to fetch pod info for %v %v ", runtimePodInfo, err)
	}
	lbc, err := newLoadBalancerController(kubeClient, *resyncPeriod, *watchNamespace, runtimePodInfo)
	if err != nil {
		glog.Fatalf("%v", err)
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
	stopLock       sync.Mutex
	shutdown       bool
	stopCh         chan struct{}
	podInfo        *podInfo
}

var (
	keyFunc = framework.DeletionHandlingMetaNamespaceKeyFunc
)

func newLoadBalancerController(kubeClient *client.Client, resyncPeriod time.Duration, namespace string, runtimeInfo *podInfo) (*loadBalancerController, error) {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(glog.Infof)
	eventBroadcaster.StartRecordingToSink(kubeClient.Events(""))
	lbc := loadBalancerController{
		client:   kubeClient,
		stopCh:   make(chan struct{}),
		podInfo:  runtimeInfo,
		recorder: eventBroadcaster.NewRecorder(api.EventSource{Component: "loadbalancer-controller"}),
	}

	lbc.syncQueue = utils.NewTaskQueue(lbc.sync)
	lbc.ingQueue = utils.NewTaskQueue(lbc.updateIngressStatus)

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
	/*if !lbc.controllersInSync() {
	      lbc.syncQueue.requeue(key, fmt.Errorf("deferring sync till endpoints controller has synced"))
	      return
	  }

	  ings := lbc.ingLister.Store.List()
	  upstreams, servers := lbc.getUpstreamServers(ings)

	  var cfg *api.ConfigMap

	  ns, name, _ := parseNsName(lbc.nxgConfigMap)
	  cfg, err := lbc.getConfigMap(ns, name)
	  if err != nil {
	      cfg = &api.ConfigMap{}
	  }

	  ngxConfig := lbc.nginx.ReadConfig(cfg)
	  lbc.nginx.CheckAndReload(ngxConfig, nginx.IngressConfig{
	      Upstreams:    upstreams,
	      Servers:      servers,
	      TCPUpstreams: lbc.getTCPServices(),
	      UDPUpstreams: lbc.getUDPServices(),
	  })*/
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
		glog.Errorf("unexpected error searching Ingress %v/%v: %v", ing.Namespace, ing.Name, err)
		return
	}

	lbIPs := ing.Status.LoadBalancer.Ingress
	if !lbc.isStatusIPDefined(lbIPs) {
		glog.Infof("Updating loadbalancer %v/%v with IP %v", ing.Namespace, ing.Name, lbc.podInfo.NodeIP)
		currIng.Status.LoadBalancer.Ingress = append(currIng.Status.LoadBalancer.Ingress, api.LoadBalancerIngress{
			IP: lbc.podInfo.NodeIP,
		})
		if _, err := ingClient.UpdateStatus(currIng); err != nil {
			lbc.recorder.Eventf(currIng, api.EventTypeWarning, "UPDATE", "error: %v", err)
			return
		}

		lbc.recorder.Eventf(currIng, api.EventTypeNormal, "CREATE", "ip: %v", lbc.podInfo.NodeIP)
	}
}

func (lbc *loadBalancerController) isStatusIPDefined(lbings []api.LoadBalancerIngress) bool {
	for _, lbing := range lbings {
		if lbing.IP == lbc.podInfo.NodeIP {
			return true
		}
	}

	return false
}

// Starts a load balancer controller
func (lbc *loadBalancerController) Run() {
	glog.Infof("starting rancher-ingress-controller")

	go lbc.ingController.Run(lbc.stopCh)
	go lbc.endpController.Run(lbc.stopCh)
	go lbc.svcController.Run(lbc.stopCh)

	go lbc.syncQueue.Run(time.Second, lbc.stopCh)
	go lbc.ingQueue.Run(time.Second, lbc.stopCh)

	<-lbc.stopCh
	glog.Infof("shutting down rancher-ingress-controller")
}

// Stop stops the loadbalancer controller.
func (lbc *loadBalancerController) Stop() error {
	// Stop is invoked from the http endpoint.
	lbc.stopLock.Lock()
	defer lbc.stopLock.Unlock()

	// Only try draining the workqueue if we haven't already.
	if !lbc.shutdown {

		lbc.removeFromIngress()

		close(lbc.stopCh)
		glog.Infof("shutting down controller queues")
		lbc.shutdown = true
		lbc.syncQueue.Shutdown()

		return nil
	}

	return fmt.Errorf("shutdown already in progress")
}

func (lbc *loadBalancerController) removeFromIngress() {
	ings := lbc.ingLister.Store.List()
	glog.Infof("updating %v Ingress rule/s", len(ings))
	for _, cur := range ings {
		ing := cur.(*extensions.Ingress)

		ingClient := lbc.client.Extensions().Ingress(ing.Namespace)
		currIng, err := ingClient.Get(ing.Name)
		if err != nil {
			glog.Errorf("unexpected error searching Ingress %v/%v: %v", ing.Namespace, ing.Name, err)
			continue
		}

		lbIPs := ing.Status.LoadBalancer.Ingress
		if len(lbIPs) > 0 && lbc.isStatusIPDefined(lbIPs) {
			glog.Infof("Updating loadbalancer %v/%v. Removing IP %v", ing.Namespace, ing.Name, lbc.podInfo.NodeIP)

			for idx, lbStatus := range currIng.Status.LoadBalancer.Ingress {
				if lbStatus.IP == lbc.podInfo.NodeIP {
					currIng.Status.LoadBalancer.Ingress = append(currIng.Status.LoadBalancer.Ingress[:idx],
						currIng.Status.LoadBalancer.Ingress[idx+1:]...)
					break
				}
			}

			if _, err := ingClient.UpdateStatus(currIng); err != nil {
				lbc.recorder.Eventf(currIng, api.EventTypeWarning, "UPDATE", "error: %v", err)
				continue
			}

			lbc.recorder.Eventf(currIng, api.EventTypeNormal, "DELETE", "ip: %v", lbc.podInfo.NodeIP)
		}
	}
}

func (lbc *loadBalancerController) GetName() string {
	return "kubernetes"
}

// podInfo contains runtime information about the pod
type podInfo struct {
	PodName      string
	PodNamespace string
	NodeIP       string
}

// returns information about current pod
func getPodDetails(kubeClient *client.Client) (*podInfo, error) {
	podName := os.Getenv("POD_NAME")
	ns := os.Getenv("POD_NAMESPACE")
	if err := waitPodRunning(kubeClient, ns, podName, time.Millisecond*200, time.Second*30); err != nil {
		return nil, err
	}
	return nil, nil
}

//waits for pod to reach a certain condition
func waitPodRunning(kubeClient *client.Client, ns string, podName string, interval, timeout time.Duration) error {
	condition := func(pod *api.Pod) bool {
		if pod.Status.Phase == api.PodRunning {
			return true
		}
		return false
	}
	return waitForPodCondition(kubeClient, ns, podName, condition, interval, timeout)
}

// waitForPodCondition waits for a pod in state defined by a condition func
func waitForPodCondition(kubeClient *client.Client, ns, podName string, condition func(pod *api.Pod) bool,
	interval, timeout time.Duration) error {
	return wait.PollImmediate(interval, timeout, func() (bool, error) {
		pod, err := kubeClient.Pods(ns).Get(podName)
		if err != nil {
			if errors.IsNotFound(err) {
				glog.Info("pod % v not found in namespace %v", podName, ns)
				return false, nil
			}
		}
		done := condition(pod)
		if done {
			return true, nil
		}

		return false, nil
	})
}
