#!/bin/bash
# Benchmark over a real network: run the server on one machine and the client on another.
#
#   server machine:  ./scripts/two_hosts.sh server <anet|net|netpoll> <port> [cpus]
#   client machine:  ./scripts/two_hosts.sh client <anet|net|netpoll> <server-ip>:<port> [concurrency] [requests] [body-bytes]
#
# Examples:
#   ./scripts/two_hosts.sh server anet 7005 0-3                   # server pinned to CPUs 0-3 (default: all)
#   ANET_INLINE=0 ./scripts/two_hosts.sh server anet 7005 0-3     # anet in goroutine mode, for comparison
#   ./scripts/two_hosts.sh client anet 10.0.0.5:7005 500 3000000 1024
#
# The client always uses the plain net client, so every server is driven by the same load generator.
# Run each configuration at least 3 times and compare medians. Check the NIC is not the limit (ethtool, sar -n DEV).
set -e
cd "$(dirname "$0")/.."
mode=$1; name=$2; target=$3
mkdir -p output/bin
case "$mode" in
server)
  go build -o output/bin/${name}_reciever ./${name}
  cpus=${4:-}
  pin=""; [ -n "$cpus" ] && pin="taskset -c $cpus"
  echo "serving $name on :$target (cpus=${cpus:-all}, ANET_INLINE=${ANET_INLINE:-default})"
  exec $pin ./output/bin/${name}_reciever -addr=":$target" -mode=1
  ;;
client)
  go build -o output/bin/net_bencher ./net/client
  c=${4:-500}; n=${5:-3000000}; b=${6:-1024}
  ./output/bin/net_bencher -name="$name" -addr="$target" -mode=1 -b=$b -c=$c -n=$n
  ;;
*)
  sed -n 2,14p "$0"; exit 1 ;;
esac
