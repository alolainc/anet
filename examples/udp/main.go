// Copyright 2024 Alola Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// This example is a UDP telemetry receiver: it counts datagrams per sender and acknowledges each one.
//
//	go run ./examples/udp               # unicast on :14550
//	go run ./examples/udp -group 239.255.0.1:14550   # join a multicast group instead
//
// Try it with: echo -n hello | nc -u -w1 127.0.0.1 14550
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"sync"

	"github.com/alolainc/anet"
)

func main() {
	addr := flag.String("addr", ":14550", "UDP address to listen on")
	group := flag.String("group", "", "multicast group to join, e.g. 239.255.0.1:14550")
	flag.Parse()

	var conn *net.UDPConn
	var err error
	if *group != "" {
		gaddr, rerr := net.ResolveUDPAddr("udp4", *group)
		if rerr != nil {
			log.Fatal(rerr)
		}
		conn, err = net.ListenMulticastUDP("udp4", nil, gaddr)
	} else {
		laddr, rerr := net.ResolveUDPAddr("udp", *addr)
		if rerr != nil {
			log.Fatal(rerr)
		}
		conn, err = net.ListenUDP("udp", laddr)
	}
	if err != nil {
		log.Fatal(err)
	}

	var mu sync.Mutex
	counts := map[netip.AddrPort]int{}
	loop, _ := anet.NewPacketLoop(func(ctx context.Context, conn *net.UDPConn, data []byte, from netip.AddrPort) {
		// runs on the poller: keep it short and copy data if it must outlive the call
		mu.Lock()
		counts[from]++
		n := counts[from]
		mu.Unlock()
		if n%1000 == 1 {
			log.Printf("%v: %d datagrams, last %d bytes", from, n, len(data))
		}
		conn.WriteToUDPAddrPort([]byte("ack"), from)
	})

	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt)
		<-stop
		loop.Shutdown(context.Background())
	}()
	log.Printf("listening on %v", conn.LocalAddr())
	if err := loop.Serve(conn); err != nil {
		log.Fatal(err)
	}
}
