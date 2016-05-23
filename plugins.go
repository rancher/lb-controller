package main

import (
	// lb controllers
	_ "github.com/rancher/ingress-controller/controller/kubernetes"

	//lb providers
	_ "github.com/rancher/ingress-controller/provider/haproxy"
	_ "github.com/rancher/ingress-controller/provider/rancher"
)
