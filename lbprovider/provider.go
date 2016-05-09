package lbprovider

import (
	"fmt"
	"github.com/rancher/rancher-ingress/lbconfig"
)

const Localhost = "localhost"

type LBProvider interface {
	ApplyConfig(lbConfig *lbconfig.LoadBalancerConfig) error
	GetName() string
	GetPublicEndpoint(configName string) string
	CleanupConfig(configName string) error
	Stop() error
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
