#!/bin/bash

#go clean --cache
go build -o /tmp/hoop-client github.com/runopsio/hoop/client
/tmp/hoop-client "$@"
