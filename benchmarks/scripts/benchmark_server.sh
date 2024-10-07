#!/bin/bash

n=5000000
body=(1024) # 1KB
concurrent=(100 500 1000)

# no need to change
repo=("net" "netpoll" "gnet" "evio" "anet" 
# "gnet_v2"
)
ports=(7001 7002 7003 7004 7005 
# 7006
)

. ./scripts/env.sh
. ./scripts/build.sh

# check args
args=${1-1}
if [[ "$args" == "3" ]]; then
  repo=("net" "netpoll" "gnet" "anet" 
  # "gnet_v2"
  )
fi

# 1. echo
benchmark "server" $args
