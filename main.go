package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rancher/lb-controller/controller"
	"github.com/rancher/lb-controller/provider"
	"github.com/rancher/log"
	logserver "github.com/rancher/log/server"
	"github.com/urfave/cli"
)

var (
	lbControllerName string
	lbProviderName   string
	metadataAddress  string

	lbc controller.LBController
	lbp provider.LBProvider
)

func main() {
	logserver.StartServerWithDefaults()

	if os.Getenv("RANCHER_DEBUG") == "true" {
		log.SetLevelString("debug")
	}
	app := cli.NewApp()

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "controller",
			Value: "kubernetes",
			Usage: "Controller plugin name",
		}, cli.StringFlag{
			Name:  "provider",
			Value: "haproxy",
			Usage: "Provider plugin name",
		}, cli.StringFlag{
			Name:   "metadata-address",
			EnvVar: "RANCHER_METADATA_ADDRESS",
			Value:  "169.254.169.250",
			Usage:  "Rancher metadata address",
		},
	}

	app.Action = func(c *cli.Context) error {
		log.Infof("Starting Rancher LB service")
		lbControllerName = c.String("controller")
		lbProviderName = c.String("provider")
		metadataAddress = c.String("metadata-address")
		lbc = controller.GetController(lbControllerName, fmt.Sprintf("http://%s/2016-07-29", metadataAddress))
		if lbc == nil {
			log.Fatalf("Unable to find controller by name %s", lbControllerName)
		}
		lbp = provider.GetProvider(lbProviderName)
		if lbp == nil {
			log.Fatalf("Unable to find provider by name %s", lbProviderName)
		}
		log.Infof("LB controller: %s", lbc.GetName())
		log.Infof("LB provider: %s", lbp.GetName())

		go handleSigterm(lbc, lbp)

		go startHealthcheck()

		lbc.Run(lbp)
		return nil
	}

	app.Run(os.Args)
}

func handleSigterm(lbc controller.LBController, lbp provider.LBProvider) {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGTERM)
	<-signalChan
	log.Infof("Received SIGTERM, shutting down")

	exitCode := 0
	// stop the controller
	if err := lbc.Stop(); err != nil {
		log.Infof("Error during shutdown %v", err)
		exitCode = 1
	}
	log.Infof("Exiting with %v", exitCode)
	os.Exit(exitCode)
}
