FROM alpine:3.2
MAINTAINER Rancher Labs, Inc.
RUN apk add --update ca-certificates 

COPY lb-controller /usr/bin/

CMD ["lb-controller"]
