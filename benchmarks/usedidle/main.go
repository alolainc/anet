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

// Command usedidle opens many connections to an echo server of this repository, sends a few requests on each
// and then keeps them open without traffic: the common state of a server's connections. It prints READY when
// all connections are idle and holds them for -hold, so that the server's memory can be measured meanwhile.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7005", "server address")
	conns := flag.Int("c", 5000, "connections")
	rounds := flag.Int("rounds", 3, "requests per connection before it goes idle")
	size := flag.Int("b", 1024, "request body size")
	hold := flag.Duration("hold", 10*time.Second, "how long to keep the idle connections")
	flag.Parse()

	body := make([]byte, 4+*size)
	binary.BigEndian.PutUint32(body, uint32(*size))
	cs := make([]net.Conn, *conns)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 200) // dial in parallel without flooding the accept queue
	errs := make(chan error, *conns)
	for i := range cs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer func() { <-sem; wg.Done() }()
			c, err := net.DialTimeout("tcp", *addr, 5*time.Second)
			if err != nil {
				errs <- err
				return
			}
			cs[i] = c
			reply := make([]byte, len(body))
			for r := 0; r < *rounds; r++ {
				if _, err := c.Write(body); err != nil {
					errs <- err
					return
				}
				if _, err := io.ReadFull(c, reply); err != nil {
					errs <- err
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	failed := 0
	for err := range errs {
		if failed == 0 {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		failed++
	}
	fmt.Printf("READY conns=%d failed=%d\n", *conns, failed)
	time.Sleep(*hold)
	for _, c := range cs {
		if c != nil {
			c.Close()
		}
	}
}
