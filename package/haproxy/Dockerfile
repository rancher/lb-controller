FROM ubuntu:15.10

RUN apt-get update && apt-get install -y \
    curl \
    vim-tiny \
    iputils-ping \
    software-properties-common && \
    add-apt-repository ppa:vbernat/haproxy-1.6 && \
    apt-get update && apt-get install -y haproxy

RUN mkdir -p /etc/haproxy/
COPY lb-controller /usr/bin/
COPY haproxy_reload /usr/bin/
COPY haproxy_template.cfg /etc/haproxy/haproxy_template.cfg

CMD ["lb-controller"]
