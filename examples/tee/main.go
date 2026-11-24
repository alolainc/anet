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

// This example broadcasts a line of text every second to every connected client:
//
//	go run ./examples/tee        # then, in other terminals: nc localhost 8083
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/alolainc/anet"
	"github.com/alolainc/anet/tee"
)

func main() {
	t := tee.New(tee.Config{
		MaxPending: 1 << 20,
		Policy:     tee.DropOldest,         // a client that falls behind skips to the newest data
		MaxAge:     500 * time.Millisecond, // and never receives a frame that waited longer than this
		OnSlow:     func(c anet.Connection, dropped bool) { log.Printf("%v is slow", c.RemoteAddr()) },
	})

	go func() {
		for tick := range time.Tick(time.Second) {
			n := t.Write([]byte(fmt.Sprintf("%s\n", tick.Format(time.RFC3339))))
			log.Printf("sent to %d clients", n)
		}
	}()

	listener, _ := anet.CreateListener("tcp", ":8083")
	eventLoop, _ := anet.NewEventLoop(nil, anet.WithOnConnect(func(ctx context.Context, c anet.Connection) context.Context {
		t.Add(c)
		return ctx
	}))
	eventLoop.Serve(listener)
}
