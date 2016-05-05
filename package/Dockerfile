FROM alpine:3.2
MAINTAINER Rancher Labs, Inc.
RUN apk add --update ca-certificates 

COPY rancher-ingress /usr/bin/

CMD ["rancher-ingress"]
