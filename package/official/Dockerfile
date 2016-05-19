FROM alpine:3.2
MAINTAINER Rancher Labs, Inc.
RUN apk add --update ca-certificates

ENV INGRESS_CONTROLLER_RELEASE v0.1.2
ADD https://github.com/rancher/ingress-controller/releases/download/${INGRESS_CONTROLLER_RELEASE}/ingress-controller.tar.gz /ingress-controller.tar.gz
RUN tar -zxvf /ingress-controller.tar.gz -C /usr/bin

CMD ["ingress-controller"]
