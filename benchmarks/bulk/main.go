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

// Command bulk measures the throughput of one anet connection sending a large amount of data, to check that a
// socket setting (see -lowat, -sndbuf) does not cost throughput on links with a large bandwidth-delay product.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/alolainc/anet"
)

var (
	mb     = flag.Int("mb", 24, "megabytes to send")
	lowat  = flag.Int("lowat", 0, "TCP_NOTSENT_LOWAT, 0 = default")
	sndbuf = flag.Int("sndbuf", 0, "SO_SNDBUF, 0 = default")
	chunk  = flag.Int("chunk", 64<<10, "write size")
)

func main() {
	flag.Parse()
	total := *mb << 20

	var opts []anet.Option
	if *lowat > 0 {
		opts = append(opts, anet.WithNotSentLowat(*lowat))
	}
	if *sndbuf > 0 {
		opts = append(opts, anet.WithSendBuffer(*sndbuf))
	}
	opts = append(opts, anet.WithOnConnect(func(ctx context.Context, c anet.Connection) context.Context {
		go func() { // OnConnect must not block
			buf := make([]byte, *chunk)
			for sent := 0; sent < total; sent += len(buf) {
				c.Writer().WriteBinary(buf)
				if err := c.Writer().Flush(); err != nil {
					return
				}
			}
		}()
		return ctx
	}))
	ln, err := anet.CreateListener("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	el, _ := anet.NewEventLoop(nil, opts...)
	go el.Serve(ln)

	start := time.Now()
	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		panic(err)
	}
	n, _ := io.CopyN(io.Discard, raw, int64(total))
	d := time.Since(start)
	fmt.Printf("lowat=%-6d sndbuf=%-7d received=%dMB in %v = %.1f Mbit/s\n", *lowat, *sndbuf, n>>20, d.Round(10*time.Millisecond), float64(n)*8/d.Seconds()/1e6)
}
