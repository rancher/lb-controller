package config

import (
	"strings"
)

type BackendServices []*BackendService
type FrontendServices []*FrontendService
type Endpoints []*Endpoint

//supported protocols
const (
	HTTPProto  = "http"
	HTTPSProto = "https"
	TLSProto   = "tls"
	TCPProto   = "tcp"
	SNIProto   = "sni"
	UDPProto   = "udp"
)

//supported comparators

const (
	EqRuleComparator  = "eq"
	BegRuleComparator = "beg"
	EndRuleComparator = "end"
)

type HealthCheck struct {
	ResponseTimeout    int    `json:"response_timeout"`
	Interval           int    `json:"interval"`
	HealthyThreshold   int    `json:"healthy_threshold"`
	UnhealthyThreshold int    `json:"unhealthy_threshold"`
	RequestLine        string `json:"request_line"`
	Port               int    `json:"port"`
}

type StickinessPolicy struct {
	Name     string `json:"name"`
	Cookie   string `json:"cookie"`
	Domain   string `json:"domain"`
	Indirect bool   `json:"indirect"`
	Nocache  bool   `json:"nocache"`
	Postonly bool   `json:"postonly"`
	Mode     string `json:"mode"`
}

type BackendService struct {
	UUID           string
	Endpoints      Endpoints
	Path           string
	Host           string
	RuleComparator string
	Algorithm      string
	Port           int
	Protocol       string
	Config         string
	HealthCheck    *HealthCheck
	Priority       int
	SendProxy      bool
}

type Endpoint struct {
	Name    string
	IP      string
	Port    int
	Config  string
	IsCname bool
	Weight  int
}

type FrontendService struct {
	Name            string
	Port            int
	BackendServices BackendServices
	Protocol        string
	Config          string
	AcceptProxy     bool
}

type LoadBalancerConfig struct {
	DefaultCert      *Certificate
	Certs            []*Certificate
	Name             string
	Annotations      map[string]string
	FrontendServices FrontendServices
	Config           string
	StickinessPolicy *StickinessPolicy
}

type Certificate struct {
	Name  string
	Cert  string
	Key   string
	Fetch bool
}

func (s FrontendServices) Len() int {
	return len(s)
}
func (s FrontendServices) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
func (s FrontendServices) Less(i, j int) bool {
	return strings.Compare(s[i].Name, s[j].Name) > 0
}

func (s BackendServices) Len() int {
	return len(s)
}
func (s BackendServices) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
func (s BackendServices) Less(i, j int) bool {
	host1 := s[i].Host
	path1 := s[i].Path
	host2 := s[j].Host
	path2 := s[j].Path
	eq1 := strings.EqualFold(s[i].RuleComparator, EqRuleComparator)
	eq2 := strings.EqualFold(s[j].RuleComparator, EqRuleComparator)

	// rules marked with priority come first
	p1 := s[i].Priority
	p2 := s[j].Priority
	if p1 > 0 {
		if p2 == 0 {
			return true
		}
		return p1 < p2
	} else if p2 > 0 {
		return false
	}

	//rules order:
	// a) with non-empty host/path
	// b) with non-empty host
	// c) with non-empty wildcard host and path
	// d) with wildcard host
	// e) with non-empty path
	if host1 != "" {
		if eq1 {
			if path1 != "" {
				return true
			} else if host2 == "" || !eq2 {
				return true
			}
		} else {
			if host2 == "" || (!eq2 && path1 != "" && path2 == "") {
				return true
			}
		}
	} else if path1 != "" {
		if host2 == "" {
			return true
		}
	}
	return false
}

func (s Endpoints) Len() int {
	return len(s)
}
func (s Endpoints) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
func (s Endpoints) Less(i, j int) bool {
	return strings.Compare(s[i].IP, s[j].IP) > 0
}
