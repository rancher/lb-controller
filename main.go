package main

import (
	"flag"
	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/client/unversioned"
	"time"
)

var (
	watchNamespace = flag.String("watch-namespace", api.NamespaceAll, "Namespace to watch for ingress. Default is ALL")
)

func main() {
	glog.Info("Starting rancher-ingress")
	kubeClient, err := unversioned.NewInCluster()
	if err != nil {
		glog.Errorf("Failed to create a client %v", err)
	}

	runtimePodInfo := &podInfo{NodeIP: "127.0.0.1"}
	runtimePodInfo, err = getPodDetails(kubeClient)
	if err != nil {
		glog.Errorf("Failed to fetch pod info for %v %v ", runtimePodInfo, err)
	}
	for {
		time.Sleep(5 * time.Second)
		glog.Info("Doing nothing")
	}
}

// podInfo contains runtime information about the pod
type podInfo struct {
	PodName      string
	PodNamespace string
	NodeIP       string
}
