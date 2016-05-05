package lbconfig

type BackendService struct {
	Name      string
	Endpoints []Endpoint
	Path      string
	Host      string
	Algorithm string
	Port      int
}

type Endpoint struct {
	IP   string
	Port int
}

type FrontendService struct {
	Name            string
	Port            int
	BackendServices []BackendService
}

type LoadBalancerConfig struct {
	Name             string
	Namespace        string
	FrontendServices []FrontendService
}
