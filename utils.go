package main

import (
	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/errors"
	"k8s.io/kubernetes/pkg/client/cache"
	"k8s.io/kubernetes/pkg/client/unversioned"
	"k8s.io/kubernetes/pkg/util/wait"
	"k8s.io/kubernetes/pkg/util/workqueue"
	"os"
	"time"
)

// returns information about current pod
func getPodDetails(kubeClient *unversioned.Client) (*podInfo, error) {
	podName := os.Getenv("POD_NAME")
	ns := os.Getenv("POD_NAMESPACE")
	if err := waitPodRunning(kubeClient, ns, podName, time.Millisecond*200, time.Second*30); err != nil {
		return nil, err
	}
	return nil, nil
}

//waits for pod to reach a certain condition
func waitPodRunning(kubeClient *unversioned.Client, ns string, podName string, interval, timeout time.Duration) error {
	condition := func(pod *api.Pod) bool {
		if pod.Status.Phase == api.PodRunning {
			return true
		}
		return false
	}
	return waitForPodCondition(kubeClient, ns, podName, condition, interval, timeout)
}

// waitForPodCondition waits for a pod in state defined by a condition func
func waitForPodCondition(kubeClient *unversioned.Client, ns, podName string, condition func(pod *api.Pod) bool,
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

// StoreToIngressLister makes a Store that lists Ingress.
type StoreToIngressLister struct {
	cache.Store
}

// TaskQueue manages a work queue through an independent worker that
// invokes the given sync function for every work item inserted.
type TaskQueue struct {
	// queue is the work queue the worker polls
	queue *workqueue.Type
	// sync is called for each item in the queue
	sync func(string)
	// workerDone is closed when the worker exits
	workerDone chan struct{}
}

func (t *TaskQueue) run(period time.Duration, stopCh <-chan struct{}) {
	wait.Until(t.worker, period, stopCh)
}

// enqueue enqueues ns/name of the given api object in the task queue.
func (t *TaskQueue) enqueue(obj interface{}) {
	key, err := keyFunc(obj)
	if err != nil {
		glog.Infof("could not get key for object %+v: %v", obj, err)
		return
	}
	t.queue.Add(key)
}

func (t *TaskQueue) requeue(key string, err error) {
	glog.V(3).Infof("requeuing %v, err %v", key, err)
	t.queue.Add(key)
}

// worker processes work in the queue through sync.
func (t *TaskQueue) worker() {
	for {
		key, quit := t.queue.Get()
		if quit {
			close(t.workerDone)
			return
		}
		glog.V(3).Infof("syncing %v", key)
		t.sync(key.(string))
		t.queue.Done(key)
	}
}

// shutdown shuts down the work queue and waits for the worker to ACK
func (t *TaskQueue) shutdown() {
	t.queue.ShutDown()
	<-t.workerDone
}

// NewTaskQueue creates a new task queue with the given sync function.
// The sync function is called for every element inserted into the queue.
func NewTaskQueue(syncFn func(string)) *TaskQueue {
	return &TaskQueue{
		queue:      workqueue.New(),
		sync:       syncFn,
		workerDone: make(chan struct{}),
	}
}
