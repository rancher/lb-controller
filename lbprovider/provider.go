package lbprovider

import (
	"fmt"
)

type LBProvider interface {
	ApplyConfig() error
	GetName() string
}

var (
	providers map[string]LBProvider
)

func GetProvider(name string) LBProvider {
	if provider, ok := providers[name]; ok {
		return provider
	}
	return providers["haproxy"]
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
