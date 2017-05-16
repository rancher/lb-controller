FROM rancher/agent-base:v0.3.0

MAINTAINER Rancher Labs, Inc.
RUN apt-get update && apt-get install -y \
    wget \
    ca-certificates \
    openssl \
    libssl-dev \
    unzip

ENV SSL_SCRIPT_COMMIT 98660ada3d800f653fc1f105771b5173f9d1a019
RUN wget -O /usr/bin/update-rancher-ssl https://raw.githubusercontent.com/rancher/rancher/${SSL_SCRIPT_COMMIT}/server/bin/update-rancher-ssl && \
    chmod +x /usr/bin/update-rancher-ssl

COPY entry.sh /usr/bin/
COPY lb-controller /usr/bin/

ENTRYPOINT ["/usr/bin/entry.sh"]
