package rancher

import (
	"encoding/json"
	"github.com/rancher/lb-controller/config"
)

type Port struct {
	SourcePort  int    `json:"source_port"`
	Protocol    string `json:"protocol"`
	Path        string `json:"path"`
	Hostname    string `json:"hostname"`
	Service     string `json:"service"`
	TargetPort  int    `json:"target_port"`
	Priority    int    `json:"priority"`
	BackendName string `json:"backend_name"`
	Selector    string `json:"selector"`
}

type LBMetadata struct {
	PortRules        []Port                  `json:"port_rules"`
	Certs            []string                `json:"certs"`
	DefaultCert      string                  `json:"default_cert"`
	Config           string                  `json:"config"`
	StickinessPolicy config.StickinessPolicy `json:"stickiness_policy"`
}

// converts an interface to LBMetadata
func getLBMetadata(data interface{}) (*LBMetadata, error) {
	lbMeta := &LBMetadata{}
	if err := convert(data, lbMeta); err != nil {
		return nil, err
	}
	return lbMeta, nil
}

func getConfigServiceHealthCheck(data interface{}) (*config.HealthCheck, error) {
	healthCheck := &config.HealthCheck{}
	if err := convert(data, healthCheck); err != nil {
		return nil, err
	}
	return healthCheck, nil
}

func convert(o1 interface{}, o2 interface{}) error {
	b, err := json.Marshal(o1)
	if err != nil {
		return err
	}
	err = json.Unmarshal(b, &o2)
	if err != nil {
		return err
	}
	return nil
}
