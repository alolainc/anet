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

//go:build linux

package anet

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alolainc/anet/internal/netpoll"
)

func echoHandler(ctx context.Context, c Connection) error {
	msg, err := c.Reader().ReadString(c.Reader().Len())
	if err != nil {
		return err
	}
	c.Writer().WriteString(msg)
	return c.Writer().Flush()
}

func TestReusePortSpreadsAccepts(t *testing.T) {
	ln, err := CreateReusePortListener("tcp", "127.0.0.1:0")
	MustNil(t, err)
	el, err := NewEventLoop(echoHandler, WithReusePort())
	MustNil(t, err)
	go el.Serve(ln)
	defer el.Shutdown(context.Background())

	evl := el.(*eventLoop)
	want := netpoll.Default.NumLoops()
	waitUntil(t, func() bool { evl.Lock(); defer evl.Unlock(); return len(evl.svrs) == want })

	// Many connections from different source ports: the kernel hashes them over the listeners.
	const n = 64
	for i := 0; i < n; i++ {
		c, err := DialConnection("tcp", ln.Addr().String(), time.Second)
		MustNil(t, err)
		defer c.Close()
		c.Writer().WriteString("x")
		MustNil(t, c.Writer().Flush())
		c.SetReadTimeout(time.Second)
		got, err := c.Reader().ReadString(1)
		MustNil(t, err)
		Equal(t, got, "x")
	}
	used := 0
	evl.Lock()
	for _, s := range evl.svrs {
		cnt := 0
		s.connections.Range(func(_, _ any) bool { cnt++; return true })
		if cnt > 0 {
			used++
		}
	}
	evl.Unlock()
	t.Logf("%d listeners, %d of them accepted connections", want, used)
	if want > 1 && used < 2 {
		t.Fatalf("connections were not spread: only %d of %d listeners accepted", used, want)
	}
}

func TestReusePortNeedsReusePortListener(t *testing.T) {
	if netpoll.Default.NumLoops() < 2 {
		t.Skip("one poller: no extra listener to open")
	}
	ln, err := CreateListener("tcp", "127.0.0.1:0") // no SO_REUSEPORT
	MustNil(t, err)
	defer ln.Close()
	el, err := NewEventLoop(echoHandler, WithReusePort())
	MustNil(t, err)
	err = el.Serve(ln)
	Assert(t, err != nil, "Serve must refuse a listener without SO_REUSEPORT")
	t.Log(err)
}

// The mode used where the kernel does not balance SO_REUSEPORT (macOS, NetBSD, OpenBSD, DragonFly): every
// poller watches the same listener. It is plain code, so it is tested here by switching the mode.
func TestReusePortSharedListener(t *testing.T) {
	if netpoll.Default.NumLoops() < 2 {
		t.Skip("one poller: nothing to share")
	}
	reusePortBalances = false
	defer func() { reusePortBalances = true }()

	ln, err := CreateListener("tcp", "127.0.0.1:0") // a plain listener is fine in this mode
	MustNil(t, err)
	addr := ln.Addr().String()
	el, err := NewEventLoop(echoHandler, WithReusePort())
	MustNil(t, err)
	go el.Serve(ln)

	evl := el.(*eventLoop)
	want := netpoll.Default.NumLoops()
	waitUntil(t, func() bool { evl.Lock(); defer evl.Unlock(); return len(evl.svrs) == want })

	for i := 0; i < 64; i++ {
		c, err := DialConnection("tcp", addr, time.Second)
		MustNil(t, err)
		defer c.Close()
		c.Writer().WriteString("y")
		MustNil(t, c.Writer().Flush())
		c.SetReadTimeout(time.Second)
		got, err := c.Reader().ReadString(1)
		MustNil(t, err)
		Equal(t, got, "y")
	}
	used := 0
	evl.Lock()
	for _, s := range evl.svrs {
		Assert(t, s.ln == ln, "every poller must share the one listener")
		n := 0
		s.connections.Range(func(_, _ any) bool { n++; return true })
		if n > 0 {
			used++
		}
	}
	evl.Unlock()
	t.Logf("%d pollers share the listener, %d of them accepted connections", want, used)
	Assert(t, used >= 2, "accepts were not spread over the pollers")

	MustNil(t, el.Shutdown(context.Background()))
	_, err = DialConnection("tcp", addr, 300*time.Millisecond)
	Assert(t, err != nil, "the shared listener must be closed after Shutdown")
}

func TestReusePortRejectsUnix(t *testing.T) {
	_, err := CreateReusePortListener("unix", "/tmp/anet-reuseport-test.sock")
	Assert(t, errors.Is(err, ErrUnsupported), err)
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
