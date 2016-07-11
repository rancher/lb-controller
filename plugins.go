package main

import (
	// lb controllers
	_ "github.com/rancher/lb-controller/controller/kubernetes"

	//lb providers
	_ "github.com/rancher/lb-controller/provider/haproxy"
	_ "github.com/rancher/lb-controller/provider/rancher"
)
