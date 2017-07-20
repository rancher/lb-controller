package provider

import (
	"fmt"
	"github.com/rancher/lb-controller/config"
	utils "github.com/rancher/lb-controller/utils"
)

const Localhost = "localhost"

type LBProvider interface {
	ApplyConfig(lbConfig *config.LoadBalancerConfig) error
	GetName() string
	GetPublicEndpoints(configName string) []string
	CleanupConfig(configName string) error
	Run(syncEndpointsQueue *utils.TaskQueue)
	Stop() error
	IsHealthy() bool
	ProcessCustomConfig(lbConfig *config.LoadBalancerConfig, customConfig string) error
}

var (
	providers map[string]LBProvider
)

func GetProvider(name string) LBProvider {
	if provider, ok := providers[name]; ok {
		return provider
	}
	return providers["rancher"]
}

func RegisterProvider(name string, provider LBProvider) error {
	if providers == nil {
		providers = make(map[string]LBProvider)
	}
	if _, exists := providers[name]; exists {
		return fmt.Errorf("provider already registered")
	}
	providers[name] = provider
	return nil
}
