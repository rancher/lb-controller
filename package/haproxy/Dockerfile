FROM ubuntu:14.04.4
ADD http://stedolan.github.io/jq/download/linux64/jq /usr/bin/
RUN chmod +x /usr/bin/jq
RUN apt-get update && apt-get install -y \
    curl \
    tcpdump \
    vim-tiny \
    openssl \
    libssl-dev \
    software-properties-common && \
    add-apt-repository ppa:vbernat/haproxy-1.6 && \
    apt-get update && apt-get install -y haproxy && \
    rm -rf /var/lib/apt/lists

RUN mkdir -p /etc/haproxy/

COPY lb-controller /usr/bin/
COPY scripts/* /usr/bin/
COPY config/* /etc/haproxy/

ENTRYPOINT ["lb-controller"]
CMD ["--controller", "rancher", "--provider", "haproxy"]
