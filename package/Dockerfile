FROM alpine:3.2
MAINTAINER Rancher Labs, Inc.
RUN apk add --update ca-certificates 

COPY ingress-controller /usr/bin/

CMD ["ingress-controller"]
