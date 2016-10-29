package main

import (
	// controllers
	_ "github.com/rancher/lb-controller/controller/kubernetes"
	_ "github.com/rancher/lb-controller/controller/rancher"

	//providers
	_ "github.com/rancher/lb-controller/provider/haproxy"
	_ "github.com/rancher/lb-controller/provider/rancher"
)
