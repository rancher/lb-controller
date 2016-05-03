package main

import (
	"github.com/golang/glog"
	"github.com/spf13/pflag"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/unversioned"
	"k8s.io/kubernetes/pkg/client/restclient"
	client "k8s.io/kubernetes/pkg/client/unversioned"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var (
	flags        = pflag.NewFlagSet("", pflag.ExitOnError)
	resyncPeriod = flags.Duration("sync-period", 30*time.Second,
		`Relist and confirm cloud resources this often.`)

	watchNamespace = flags.String("watch-namespace", api.NamespaceAll,
		`Namespace to watch for Ingress. Default is to watch all namespaces`)
)

func main() {
	glog.Info("Starting rancher-ingress")
	config := &restclient.Config{
		Host:          os.Getenv("KUBERNETES_SERVER"),
		ContentConfig: restclient.ContentConfig{GroupVersion: &unversioned.GroupVersion{Version: "v1"}},
	}
	kubeClient, err := client.New(config)

	if err != nil {
		glog.Fatalf("failed to create client: %v", err)
	}

	runtimePodInfo, err := getPodDetails(kubeClient)
	if err != nil {
		glog.Errorf("Failed to fetch pod info for %v %v ", runtimePodInfo, err)
	}
	lbc, err := newLoadBalancerController(kubeClient, *resyncPeriod, *watchNamespace, runtimePodInfo)
	if err != nil {
		glog.Fatalf("%v", err)
	}

	go handleSigterm(lbc)

	lbc.Run()
}

// podInfo contains runtime information about the pod
type podInfo struct {
	PodName      string
	PodNamespace string
	NodeIP       string
}

func handleSigterm(lbc *loadBalancerController) {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGTERM)
	<-signalChan
	glog.Infof("Received SIGTERM, shutting down")

	exitCode := 0
	if err := lbc.Stop(); err != nil {
		glog.Infof("Error during shutdown %v", err)
		exitCode = 1
	}
	glog.Infof("Exiting with %v", exitCode)
	os.Exit(exitCode)
}
