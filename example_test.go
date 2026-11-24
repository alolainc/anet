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

package anet_test

import (
	"context"
	"fmt"
	"time"

	"github.com/alolainc/anet"
)

// An echo server and client. The handler runs on the poller, so it only does quick work.
func Example() {
	ln, err := anet.CreateListener("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	el, _ := anet.NewEventLoop(func(ctx context.Context, c anet.Connection) error {
		msg, _ := c.Reader().ReadString(c.Reader().Len())
		c.Writer().WriteString(msg)
		return c.Writer().Flush()
	})
	go el.Serve(ln)
	defer el.Shutdown(context.Background())

	conn, err := anet.DialConnection("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	conn.Writer().WriteString("hello")
	conn.Writer().Flush()
	conn.SetReadTimeout(time.Second)
	reply, _ := conn.Reader().ReadString(5)
	fmt.Println(reply)
	// Output: hello
}

// A handler that has to wait for data or for another service opts out of running on the poller.
func ExampleWithGoroutineRequest() {
	handler := func(ctx context.Context, c anet.Connection) error {
		header, err := c.Reader().Next(4) // waits until 4 bytes arrived
		if err != nil {
			return err
		}
		_ = header
		return nil
	}
	el, _ := anet.NewEventLoop(handler, anet.WithGoroutineRequest())
	_ = el
}

// Keep the kernel send queue short so a slow network adds little latency.
func ExampleSocketTuner() {
	conn, err := anet.DialConnection("tcp", "127.0.0.1:8080", time.Second)
	if err != nil {
		return
	}
	if st, ok := conn.(anet.SocketTuner); ok {
		st.SetNotSentLowat(16 << 10)
	}
}
