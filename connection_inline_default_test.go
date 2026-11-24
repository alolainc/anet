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

//go:build !windows

package anet

import (
	"context"
	"testing"
	"time"
)

func TestInlineIsDefault(t *testing.T) {
	cases := []struct {
		name string
		ops  []Option
		want bool
	}{
		{"default", nil, true},
		{"goroutine", []Option{WithGoroutineRequest()}, false},
		{"onconnect skips default", []Option{WithOnConnect(func(ctx context.Context, c Connection) context.Context { return ctx })}, false},
		{"onconnect with explicit inline", []Option{WithInlineRequest(), WithOnConnect(func(ctx context.Context, c Connection) context.Context { return ctx })}, true},
	}
	for _, tc := range cases {
		ev, err := NewEventLoop(nil, tc.ops...)
		if err != nil {
			t.Fatal(err)
		}
		if got := ev.(*eventLoop).opts.inlineOn(); got != tc.want {
			t.Errorf("%s: inline=%v, want %v", tc.name, got, tc.want)
		}
	}
}

// A handler that blocks works with WithGoroutineRequest, and does not stall other connections.
func TestGoroutineRequestAllowsBlockingHandler(t *testing.T) {
	network, address := "tcp", getTestAddress()
	release := make(chan struct{})
	loop := newTestEventLoop(network, address, func(ctx context.Context, c Connection) error {
		b, err := c.Reader().Next(4) // waits for data
		if err != nil {
			return err
		}
		<-release
		c.Writer().WriteBinary(b)
		return c.Writer().Flush()
	}, WithGoroutineRequest())
	defer loop.Shutdown(context.Background())

	conn, err := DialConnection(network, address, time.Second)
	MustNil(t, err)
	defer conn.Close()
	conn.Write([]byte("ab"))
	time.Sleep(50 * time.Millisecond)
	conn.Write([]byte("cd")) // second half arrives later, the handler waits for it
	close(release)
	conn.SetReadTimeout(2 * time.Second)
	got, err := conn.Reader().Next(4)
	MustNil(t, err)
	Equal(t, string(got), "abcd")
}
