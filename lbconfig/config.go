package lbconfig

type BackendService struct {
	Name         string
	Endpoints    []string
	Path         string
	Host         string
	Algorithm    string
	Port         int
	FrontendPort int
}

type FrontendService struct {
	Port            int
	BackendServices []BackendService
}

type LoadBalancerConfig struct {
	FrontendServices []FrontendService
	BackendServices  []BackendService
}
