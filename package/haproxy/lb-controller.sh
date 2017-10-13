#!/bin/bash

# to solve this issue https://github.com/rancher/rancher/issues/10074
while ! curl -s -f http://rancher-metadata/2015-12-19/self/service/uuid; do
    echo Waiting for metadata self service
    sleep 1
done

/usr/bin/update-rancher-ssl

exec lb-controller $@
