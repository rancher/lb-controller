package lbprovider

import (
	"fmt"
	"github.com/golang/glog"
	"github.com/rancher/go-rancher/client"
	"github.com/rancher/rancher-ingress/lbconfig"
	"os"
)

const (
	lbNameFormat      string = "lb-%s"
	lbStackName       string = "kubernetes-ingress-loadbalancers"
	lbStackExternalID string = "kubernetes-ingress-loadbalancers://"
)

func init() {
	var config string
	if config = os.Getenv("HAPROXY_CONFIG"); len(config) == 0 {
		glog.Info("HAPROXY_CONFIG is not set, skipping init of haproxy provider")
		return
	}

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

func (lbc *RancherLBProvider) ApplyConfig(lbName string, lbConfig *lbconfig.LoadBalancerConfig) error {
	_, err := lbc.getLBServiceByName(lbName)
	if err != nil {
		return err
	}
	return nil
}

func (lbc *RancherLBProvider) GetName() string {
	return "rancher"
}

func (lbc *RancherLBProvider) GetPublicEndpoint(lbName string) string {
	return Localhost
}

func (lbc *RancherLBProvider) getStackByName(name string) (*client.Environment, error) {
	opts := client.NewListOpts()
	opts.Filters["name"] = name
	opts.Filters["removed_null"] = "1"
	lbs, err := lbc.client.Environment.List(opts)
	if err != nil {
		return nil, fmt.Errorf("Coudln't get LB by name [%s]. Error: %#v", name, err)
	}

	if len(lbs.Data) == 0 {
		return nil, nil
	}

	return &lbs.Data[0], nil
}

func (lbc *RancherLBProvider) getLBServiceByName(name string) (*client.LoadBalancerService, error) {
	stack, err := lbc.getStackByName(lbStackName)
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
