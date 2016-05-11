package main

import (
	"flag"
	"github.com/Sirupsen/logrus"
	"github.com/rancher/ingress-controller/lbcontroller"
	"github.com/rancher/ingress-controller/lbprovider"
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
	if lbc == nil {
		logrus.Fatalf("Unable to find controller by name %s", *lbControllerName)
	}
	lbp = lbprovider.GetProvider(*lbProviderName)
	if lbp == nil {
		logrus.Fatalf("Unable to find provider by name %s", *lbProviderName)
	}
}

func main() {
	logrus.Infof("Starting Rancher LB service")
	setEnv()
	logrus.Infof("LB controller: %s", lbc.GetName())
	logrus.Infof("LB provider: %s", lbp.GetName())

	go handleSigterm(lbc, lbp)

	go startHealthcheck()

	lbc.Run(lbp)
}

func handleSigterm(lbc lbcontroller.LBController, lbp lbprovider.LBProvider) {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGTERM)
	<-signalChan
	logrus.Infof("Received SIGTERM, shutting down")

	exitCode := 0
	// stop the controller
	if err := lbc.Stop(); err != nil {
		logrus.Infof("Error during shutdown %v", err)
		exitCode = 1
	}
	logrus.Infof("Exiting with %v", exitCode)
	os.Exit(exitCode)
}
