FROM alpine:3.2
MAINTAINER Rancher Labs, Inc.
RUN apk add --update ca-certificates

ENV LB_CONTROLLER_RELEASE v0.2.0
ADD https://github.com/rancher/lb-controller/releases/download/${LB_CONTROLLER_RELEASE}/lb-controller.tar.gz /lb-controller.tar.gz
RUN tar -zxvf /lb-controller.tar.gz -C /usr/bin

CMD ["lb-controller"]
