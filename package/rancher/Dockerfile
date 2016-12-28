FROM alpine:3.2
MAINTAINER Rancher Labs, Inc.
RUN apk add --update ca-certificates wget
RUN update-ca-certificates
ENV SSL_SCRIPT_COMMIT 98660ada3d800f653fc1f105771b5173f9d1a019
RUN wget -O /usr/bin/update-rancher-ssl https://raw.githubusercontent.com/rancher/rancher/${SSL_SCRIPT_COMMIT}/server/bin/update-rancher-ssl && \
    chmod +x /usr/bin/update-rancher-ssl
COPY lb-controller.sh /usr/bin/
COPY lb-controller /usr/bin/

CMD ["lb-controller.sh"]
