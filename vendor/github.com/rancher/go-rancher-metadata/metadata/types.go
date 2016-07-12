package metadata

type Stack struct {
	EnvironmentName string    `json:"environment_name"`
	EnvironmentUUID string    `json:"environment_uuid"`
	Name            string    `json:"name"`
	Services        []Service `json:"services"`
}

type HealthCheck struct {
	HealthyThreshold   int    `json:"healthy_threshold"`
	Interval           int    `json:"interval"`
	Port               int    `json:"port"`
	RequestLine        string `json:"request_line"`
	ResponseTimeout    int    `json:"response_timeout"`
	UnhealthyThreshold int    `json:"unhealthy_threshold"`
}

type Service struct {
	Scale       int                    `json:"scale"`
	Name        string                 `json:"name"`
	StackName   string                 `json:"stack_name"`
	Kind        string                 `json:"kind"`
	Hostname    string                 `json:"hostname"`
	Vip         string                 `json:"vip"`
	CreateIndex int                    `json:"create_index"`
	UUID        string                 `json:"uuid"`
	ExternalIps []string               `json:"external_ips"`
	Sidekicks   []string               `json:"sidekicks"`
	Containers  []Container            `json:"containers"`
	Ports       []string               `json:"ports"`
	Labels      map[string]string      `json:"labels"`
	Links       map[string]string      `json:"links"`
	Metadata    map[string]interface{} `json:"metadata"`
	Token       string                 `json:"token"`
	Fqdn        string                 `json:"fqdn"`
	HealthCheck HealthCheck            `json:"health_check"`
}

type Container struct {
	Name        string            `json:"name"`
	PrimaryIp   string            `json:"primary_ip"`
	Ips         []string          `json:"ips"`
	Ports       []string          `json:"ports"`
	ServiceName string            `json:"service_name"`
	StackName   string            `json:"stack_name"`
	Labels      map[string]string `json:"labels"`
	CreateIndex int               `json:"create_index"`
	HostUUID    string            `json:"host_uuid"`
	UUID        string            `json:"uuid"`
	State       string            `json:"state"`
	HealthState string            `json:"health_state"`
	ExternalId  string            `json:"external_id"`
}

type Host struct {
	Name     string            `json:"name"`
	AgentIP  string            `json:"agent_ip"`
	HostId   int               `json:"host_id"`
	Labels   map[string]string `json:"labels"`
	UUID     string            `json:"uuid"`
	Hostname string            `json:"hostname"`
}
