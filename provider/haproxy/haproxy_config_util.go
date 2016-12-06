package haproxy

import (
	"fmt"
	"github.com/rancher/lb-controller/config"
	"sort"
	"strings"
)

func GetDefaultConfig() map[string]map[string]string {
	defaults := make(map[string]string)
	global := make(map[string]string)
	backend := make(map[string]string)

	global["maxconn"] = "4096"
	global["maxpipes"] = "1024"
	global["tune.ssl.default-dh-param"] = "2048"
	global["ssl-default-bind-ciphers"] = "ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-AES256-GCM-SHA384:DHE-RSA-AES128-GCM-SHA256:DHE-DSS-AES128-GCM-SHA256:kEDH+AESGCM:ECDHE-RSA-AES128-SHA256:ECDHE-ECDSA-AES128-SHA256:ECDHE-RSA-AES128-SHA:ECDHE-ECDSA-AES128-SHA:ECDHE-RSA-AES256-SHA384:ECDHE-ECDSA-AES256-SHA384:ECDHE-RSA-AES256-SHA:ECDHE-ECDSA-AES256-SHA:DHE-RSA-AES128-SHA256:DHE-RSA-AES128-SHA:DHE-DSS-AES128-SHA256:DHE-RSA-AES256-SHA256:DHE-DSS-AES256-SHA:DHE-RSA-AES256-SHA:ECDHE-RSA-DES-CBC3-SHA:ECDHE-ECDSA-DES-CBC3-SHA:AES128-GCM-SHA256:AES256-GCM-SHA384:AES128-SHA256:AES256-SHA256:AES128-SHA:AES256-SHA:AES:CAMELLIA:DES-CBC3-SHA:!aNULL:!eNULL:!EXPORT:!DES:!RC4:!MD5:!PSK:!aECDH:!EDH-DSS-DES-CBC3-SHA:!EDH-RSA-DES-CBC3-SHA:!KRB5-DES-CBC3-SHA"
	global["ssl-default-server-ciphers"] = "ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-AES256-GCM-SHA384:DHE-RSA-AES128-GCM-SHA256:DHE-DSS-AES128-GCM-SHA256:kEDH+AESGCM:ECDHE-RSA-AES128-SHA256:ECDHE-ECDSA-AES128-SHA256:ECDHE-RSA-AES128-SHA:ECDHE-ECDSA-AES128-SHA:ECDHE-RSA-AES256-SHA384:ECDHE-ECDSA-AES256-SHA384:ECDHE-RSA-AES256-SHA:ECDHE-ECDSA-AES256-SHA:DHE-RSA-AES128-SHA256:DHE-RSA-AES128-SHA:DHE-DSS-AES128-SHA256:DHE-RSA-AES256-SHA256:DHE-DSS-AES256-SHA:DHE-RSA-AES256-SHA:ECDHE-RSA-DES-CBC3-SHA:ECDHE-ECDSA-DES-CBC3-SHA:AES128-GCM-SHA256:AES256-GCM-SHA384:AES128-SHA256:AES256-SHA256:AES128-SHA:AES256-SHA:AES:CAMELLIA:DES-CBC3-SHA:!aNULL:!eNULL:!EXPORT:!DES:!RC4:!MD5:!PSK:!aECDH:!EDH-DSS-DES-CBC3-SHA:!EDH-RSA-DES-CBC3-SHA:!KRB5-DES-CBC3-SHA"
	global["ssl-default-bind-options"] = "no-sslv3 no-tlsv10"
	global["chroot"] = "/var/lib/haproxy"
	global["user"] = "haproxy"
	global["group"] = "haproxy"
	global["daemon"] = ""
	global["log 127.0.0.1:8514 local0"] = ""
	global["log 127.0.0.1:8514 local1"] = "notice"

	defaults["mode"] = "tcp"
	defaults["option redispatch"] = ""
	defaults["option http-server-close"] = ""
	defaults["option forwardfor"] = ""
	defaults["maxconn"] = "4096"
	defaults["retries"] = "3"
	defaults["timeout connect"] = "5000"
	defaults["timeout client"] = "50000"
	defaults["timeout server"] = "50000"
	defaults["errorfile 400"] = "/etc/haproxy/errors/400.http"
	defaults["errorfile 403"] = "/etc/haproxy/errors/403.http"
	defaults["errorfile 408"] = "/etc/haproxy/errors/408.http"
	defaults["errorfile 500"] = "/etc/haproxy/errors/500.http"
	defaults["errorfile 502"] = "/etc/haproxy/errors/502.http"
	defaults["errorfile 503"] = "/etc/haproxy/errors/503.http"
	defaults["errorfile 504"] = "/etc/haproxy/errors/504.http"
	defaults["log"] = "global"
	defaults["option"] = "tcplog"

	backend["http-request add-header X-Forwarded-Proto"] = "https if { ssl_fc } forwarded_proto"
	backend["http-request add-header X-Forwarded-Port"] = "%[dst_port] if forwarded_port"

	config := make(map[string]map[string]string)
	config["defaults"] = defaults
	config["global"] = global
	config["backend"] = backend
	return config
}

/*BuildCustomConfig reads custom config and updates appropriate parts of lbConfig

Custom config example:

global
    maxconn 4096
    maxpipes 1024

defaults
    log global
    mode    tcp
    option  tcplog

frontend 80
    balance leastconn

frontend 90
    balance roundrobin

backend mystack_foo
    cookie my_cookie insert indirect nocache postonly
    server $IP <server parameters>

backend customUUID
    server $IP <server parameters>
*/

var keywords = []string{"backend", "frontend", "global", "defaults", "listen", "userlist", "peers", "mailers"}

func isDirective(directive string) bool {
	for _, keyword := range keywords {
		if strings.EqualFold(directive, keyword) {
			return true
		}
	}
	return false
}

func BuildCustomConfig(lbConfig *config.LoadBalancerConfig, customConfig string) error {
	customConfigMap := make(map[string]sort.StringSlice)
	var key string
	defaultConfig := GetDefaultConfig()
	serverPrefix := "server $IP"
	for _, conf := range strings.Split(customConfig, "\n") {
		conf = strings.TrimSpace(conf)
		keys := strings.SplitN(conf, " ", 2)
		prefix := keys[0]
		if strings.TrimSpace(prefix) == "" {
			continue
		}
		if isDirective(prefix) || strings.HasPrefix(conf, serverPrefix) {
			//server config comes in one line, so process it differently
			if strings.HasPrefix(conf, serverPrefix) {
				// the key would be a backend at this point
				// so the server key would become backendUUID_$IP
				backendKey := fmt.Sprintf("%s_$IP", key)
				backendValue := ""
				serverConf := strings.SplitN(conf, serverPrefix, 2)
				if len(serverConf) > 1 {
					backendValue = serverConf[1]
				}
				customConfigMap[backendKey] = append(customConfigMap[backendKey], backendValue)
				continue
			} else if len(keys) > 1 {
				key = fmt.Sprintf("%s %s", strings.TrimSpace(keys[0]), strings.TrimSpace(keys[1]))
			} else {
				key = prefix
			}
			continue
		}
		customConfigMap[key] = append(customConfigMap[key], conf)
		//exclude values from defalt configs if they are present in custom

		for k := range defaultConfig[key] {
			if strings.EqualFold(strings.TrimSpace(conf), key) || strings.HasPrefix(strings.TrimSpace(conf), fmt.Sprintf("%s ", k)) {
				delete(defaultConfig[key], k)
			}
		}
	}

	// append whatever left in default config
	for k := range defaultConfig {
		for defaultKey, defaultValue := range defaultConfig[k] {
			value := fmt.Sprintf("%s %s", strings.TrimSpace(defaultKey), strings.TrimSpace(defaultValue))
			customConfigMap[k] = append(customConfigMap[k], value)
		}
	}
	processedConfigs := make(map[string]string)
	for _, fe := range lbConfig.FrontendServices {
		var policy *config.StickinessPolicy
		policyNotNull := lbConfig.StickinessPolicy != nil && lbConfig.StickinessPolicy.Mode != ""
		policyProto := strings.EqualFold(fe.Protocol, config.HTTPSProto) || strings.EqualFold(fe.Protocol, config.HTTPProto)
		if policyNotNull && policyProto {
			policy = lbConfig.StickinessPolicy
		}
		feConfigName := fmt.Sprintf("%s %s", "frontend", fe.Name)
		var custom []string
		for _, value := range customConfigMap[feConfigName] {
			if strings.EqualFold(value, "accept-proxy") {
				fe.AcceptProxy = true
				continue
			}
			custom = append(custom, value)
		}

		//TODO - have to add support for custom bind parameters
		fe.Config = confToString(custom)
		processedConfigs[feConfigName] = ""
		for _, be := range fe.BackendServices {
			healthcheck := false
			if be.HealthCheck != nil && be.HealthCheck.Port > 0 {
				healthcheck = true
			}

			beConfig := sort.StringSlice{}
			beConfigName := fmt.Sprintf("%s %s", "backend", be.UUID)
			processedConfigs[beConfigName] = ""
			beConfig = append(beConfig, customConfigMap[beConfigName]...)
			beConfig = append(beConfig, customConfigMap["backend"]...)
			be.Config = confToString(beConfig)
			//append healthcheck
			if healthcheck {
				be.Config = fmt.Sprintf("%s\n    timeout check %v", be.Config, be.HealthCheck.ResponseTimeout)
				if be.HealthCheck.RequestLine != "" {
					be.Config = fmt.Sprintf("%s\n    option httpchk %s", be.Config, be.HealthCheck.RequestLine)
				}
			}
			//append cookie policy
			if policy != nil {
				if policy.Cookie == "" {
					policy.Cookie = be.UUID
				}
				cookieLine := fmt.Sprintf("cookie %s_%s %s", policy.Cookie, be.UUID, policy.Mode)
				if policy.Indirect {
					cookieLine = fmt.Sprintf("%s indirect", cookieLine)
				}
				if policy.Nocache {
					cookieLine = fmt.Sprintf("%s nocache", cookieLine)
				}
				if policy.Postonly {
					cookieLine = fmt.Sprintf("%s postonly", cookieLine)
				}
				if policy.Domain != "" {
					cookieLine = fmt.Sprintf("%s domain %s", cookieLine, policy.Domain)
				}
				be.Config = fmt.Sprintf("%s\n    %s", be.Config, cookieLine)
			}

			for _, ep := range be.Endpoints {
				epConfigName := fmt.Sprintf("%s_$IP", beConfigName)
				ep.Config = confToString(customConfigMap[epConfigName])
				processedConfigs[epConfigName] = ""
				//append health check
				if healthcheck {
					hc := fmt.Sprintf("check port %v inter %v rise %v fall %v", be.HealthCheck.Port, be.HealthCheck.Interval, be.HealthCheck.HealthyThreshold, be.HealthCheck.UnhealthyThreshold)
					ep.Config = fmt.Sprintf("%s %s", ep.Config, hc)
				}
				//append cookie policy
				if policy != nil {
					ep.Config = fmt.Sprintf("%s cookie %s", ep.Config, ep.Name)
				}
			}
		}
	}

	// append non-processed config
	var extraConfig string
	for k, v := range customConfigMap {
		if strings.EqualFold(k, "global") || strings.EqualFold(k, "backend") || strings.EqualFold(k, "defaults") {
			continue
		}
		if _, ok := processedConfigs[k]; !ok {
			extraConfig = fmt.Sprintf("%s\n%s\n", k, confToString(v))
		}
	}

	lbConfig.Config = fmt.Sprintf("global\n%s\n\ndefaults\n%s", confToString(customConfigMap["global"]), confToString(customConfigMap["defaults"]))
	if extraConfig != "" {
		lbConfig.Config = fmt.Sprintf("%s\n\n%s", lbConfig.Config, extraConfig)
	}

	return nil
}

func confToString(conf sort.StringSlice) string {
	if len(conf) == 0 {
		return ""
	}
	conf.Sort()
	return fmt.Sprintf("    %s", strings.Join(conf, "\n    "))
}
