package main

import (
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/errors"
	"k8s.io/kubernetes/pkg/client/unversioned"
	"k8s.io/kubernetes/pkg/util/wait"
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
