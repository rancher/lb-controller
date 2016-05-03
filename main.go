package main

import (
	"flag"
	"github.com/golang/glog"
	"github.com/rancher/rancher-ingress/lbcontroller"
	"github.com/rancher/rancher-ingress/lbprovider"
	"os"
	"os/signal"
	"syscall"
)

var (
	lbControllerName = flag.String("lb-controller", "kubernetes", "Ingress controller name")
	lbProviderName   = flag.String("lb-provider", "haproxy", "Lb controller name")

	lbc lbcontroller.LBController
	lbp lbprovider.LBProvider
)

func setEnv() {
	flag.Parse()
	lbc = lbcontroller.GetController(*lbControllerName)
	lbp = lbprovider.GetProvider(*lbProviderName)
}

func main() {
	glog.Infof("Starting Rancher LB service")
	setEnv()
	glog.Infof("LB controller: %s", lbc.GetName())
	glog.Infof("LB provider: %s", lbp.GetName())

	go handleSigterm(lbc)

	lbc.Run(lbp)
}

func handleSigterm(lbc lbcontroller.LBController) {
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
