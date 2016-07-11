# LB controller

L7 Load Balancer service managing load balancer provider configured via load balancer controller.
Pluggable model allows different controller and provider implementation. v0.1.0 has support for Kubernetes ingress as a controller, and Rancher Load Balancer as a provider. 
Rancher provider is a default one, although you can develop and deploy your own implementation (nginx, traefic, etc). 

# Design

* LB-controller gets deployed as a containerized app with controller and provider names passed as an argument
* Controler listens to the corresponding server events, generate load balancer config and pass it to the provider to apply.
* Provider configures external load balancer, and pass LB public end point to the controller. 
* Controller propagates LB public end point back to the server.
* Controller doesn't carry any provider implementation details; it communicates with the provider via generic provider interface using generic LB config.

# Kubernetes as an LB controller and Rancher as an LB provider

When Kubernetes is passed as an LB controller argument, the app would be deployed to work as a Kubernetes Ingress controller.
The controller listens to Kubernetes server events like:

* Ingress create/update/remove
* Backend services create/remove
* Backend services' endpoint changes 

and generates LB config based on the Kubernetes ingress info. After config is generated, it gets passed to LB provider - Rancher provider in our case.
The provider will create Rancher Load Balancer service for every Kubernetes ingress, and propagate Load Balancer public endpoint(s) back to the Controller.
The controller in turn would update Kubernetes ingress with the Address = Rancher Load Balancer public endpoint (ip address of the host where Rancher Load Balancer is deployed):

```
> kubectl get ingress
NAME      RULE          BACKEND   ADDRESS
test      -                        104.154.107.202 // host ip address where Rancher LB is deployed
          foo.bar.com
          /foo           nginx-service:80

```


Rancher Load Balancer provider:

* Configures Rancher LB with hostname routing rules and backend services defined in Kubernetes ingress.
* Monitors Rancher LB public endpoint changes(LB instance gets redeployed on another host) and report them back to controller, so Kubernetes ingress will be updated accordingly.
* Manages Rancher LB lifecycle - destroy LB when ingress is removed, create LB once new ingress is added, update LB config when ingress is updated

Refer to [kubernetes-ingress](//kubernetes.io/docs/user-guide/ingress/) and [kubernetes ingress-controller](//github.com/kubernetes/contrib/blob/master/ingress/controllers/README.md) for more info on Kubernetes ingress and ingress controller implementation solutions.

# Build LB controller

You can build LB controller using [Rancher dapper tool](//github.com/rancher/dapper). Just install Dapper, and run the command below from lb-controller directory:

```
dapper
```

it would build the binaries and create an lb-controller image.


# Deploy LB controller

LB controller with Kubernetes/Rancher support can be deployed as:

* part of Rancher system Kubernetes stack (recommended and officially supported way)
* as a pod container in Kubernetes deployed through Rancher with ability to access Rancher server API.

# Extra features

* Rancher Load Balancer can be horizontally scaled by specifying desired scale via ingress annotations:

```
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: scaledlb
  annotations:
    scale: "2"
    http.port: "99"
spec:
  backend:
    serviceName: nginx-service
    servicePort: 90
```

as a result ingress gets updated with multiple public addresses end points:

```
> kubectl get ingress
NAME      RULE          BACKEND   ADDRESS
test      -                        104.154.107.202, 104.154.107.203  // hosts ip addresses where Rancher LB instances are deployed
          foo.bar.com
          /foo           nginx-service:80

```

* By default, Kubernetes Ingress supports 2 public ports for Ingress: 80 and 443. With Rancher Ingress controller, you can define an alternative ports for both http and https:

```
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: scaledlb
  annotations:
    http.port: "99"
    https.port: "444"
spec:
  backend:
    serviceName: nginx-service
    servicePort: 90
```

More info on the above can be found [here](http://docs.rancher.com/rancher/latest/en/kubernetes/ingress/)


# To fix in the future release

* TLS is supported by Rancher with the only one limitation: there is no SNI routing support. We are going to address it as a part of Load Balancer refacgtoring and integrate with Ingress resource as soon as it's done.
* Support for TCP Load balancer
* Possible integration with Route53 provider. LB service FQDN populated by Rancher Route53 service, will be propagated as an entry point for the ingress.
