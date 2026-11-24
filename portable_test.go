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

package anet

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These tests exercise the portable (Windows fallback) backend over real loopback TCP.
// The backend has no OS specific code, so it can be tested on any platform.

func portableServe(t *testing.T, onRequest OnRequest, ops ...Option) (addr string, el EventLoop) {
	t.Helper()
	ln, err := newStdListener("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	el = newStdEventLoop(onRequest, ops...)
	go el.Serve(ln)
	return ln.Addr().String(), el
}

func portableEcho(ctx context.Context, c Connection) error {
	n := c.Reader().Len()
	b, err := c.Reader().ReadBinary(n)
	if err != nil {
		return err
	}
	c.Reader().Release()
	if _, err = c.Writer().WriteBinary(b); err != nil {
		return err
	}
	return c.Writer().Flush()
}

func portableDial(t *testing.T, addr string) Connection {
	t.Helper()
	c, err := newStdDialer().DialConnection("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPortableEchoSmall(t *testing.T) {
	addr, el := portableServe(t, portableEcho)
	defer el.Shutdown(context.Background())
	c := portableDial(t, addr)
	defer c.Close()

	msg := []byte("hello portable")
	if _, err := c.Write(msg); err != nil {
		t.Fatal(err)
	}
	c.SetReadTimeout(2 * time.Second)
	got, err := c.Reader().ReadBinary(len(msg))
	if err != nil || !bytes.Equal(got, msg) {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestPortableEchoLarge(t *testing.T) {
	addr, el := portableServe(t, portableEcho)
	defer el.Shutdown(context.Background())
	c := portableDial(t, addr)
	defer c.Close()

	msg := make([]byte, 4<<20)
	for i := range msg {
		msg[i] = byte(i * 7)
	}
	c.SetReadTimeout(10 * time.Second)
	errc := make(chan error, 1)
	go func() {
		_, err := c.Write(msg)
		errc <- err
	}()
	got, err := c.Reader().ReadBinary(len(msg))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatal("payload corrupted")
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestPortableManyConnections(t *testing.T) {
	addr, el := portableServe(t, portableEcho)
	defer el.Shutdown(context.Background())

	const conns, rounds = 40, 50
	var wg sync.WaitGroup
	var failed int32
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := newStdDialer().DialConnection("tcp", addr, time.Second)
			if err != nil {
				atomic.AddInt32(&failed, 1)
				return
			}
			defer c.Close()
			c.SetReadTimeout(5 * time.Second)
			for r := 0; r < rounds; r++ {
				msg := bytes.Repeat([]byte{byte(i + r)}, 100+r)
				if _, err := c.Write(msg); err != nil {
					atomic.AddInt32(&failed, 1)
					return
				}
				got, err := c.Reader().ReadBinary(len(msg))
				if err != nil || !bytes.Equal(got, msg) {
					atomic.AddInt32(&failed, 1)
					return
				}
				c.Reader().Release()
			}
		}(i)
	}
	wg.Wait()
	if failed != 0 {
		t.Fatalf("%d connections failed", failed)
	}
}

func TestPortableReadTimeout(t *testing.T) {
	addr, el := portableServe(t, nil) // no OnRequest: server just holds the connection
	defer el.Shutdown(context.Background())
	c := portableDial(t, addr)
	defer c.Close()

	c.SetReadTimeout(100 * time.Millisecond)
	start := time.Now()
	_, err := c.Reader().Next(1)
	if !errors.Is(err, ErrReadTimeout) {
		t.Fatalf("want ErrReadTimeout, got %v", err)
	}
	if d := time.Since(start); d < 80*time.Millisecond || d > time.Second {
		t.Fatalf("unexpected wait %v", d)
	}
}

func TestPortablePeerClose(t *testing.T) {
	addr, el := portableServe(t, func(ctx context.Context, c Connection) error {
		c.Reader().Skip(c.Reader().Len())
		return c.Close() // server closes after the first request
	})
	defer el.Shutdown(context.Background())
	c := portableDial(t, addr)
	defer c.Close()

	c.Write([]byte("x"))
	c.SetReadTimeout(2 * time.Second)
	_, err := c.Reader().Next(1)
	if !errors.Is(err, ErrEOF) {
		t.Fatalf("want ErrEOF after peer close, got %v", err)
	}
	if c.IsActive() {
		t.Fatal("connection should not be active after peer close")
	}
}

func TestPortableCallbackOrder(t *testing.T) {
	var mu sync.Mutex
	var events []string
	rec := func(s string) {
		mu.Lock()
		events = append(events, s)
		mu.Unlock()
	}
	disc := make(chan struct{})
	closed := make(chan struct{})
	addr, el := portableServe(t, func(ctx context.Context, c Connection) error {
		rec("request")
		c.Reader().Skip(c.Reader().Len())
		return nil
	},
		WithOnConnect(func(ctx context.Context, c Connection) context.Context {
			rec("connect")
			c.AddCloseCallback(func(Connection) error { rec("closecb"); close(closed); return nil })
			return ctx
		}),
		WithOnDisconnect(func(ctx context.Context, c Connection) { rec("disconnect"); close(disc) }),
	)
	defer el.Shutdown(context.Background())

	c := portableDial(t, addr)
	c.Write([]byte("hi"))
	time.Sleep(100 * time.Millisecond)
	c.Close()
	select {
	case <-disc:
	case <-time.After(2 * time.Second):
		t.Fatal("OnDisconnect not called")
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("close callback not called")
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"connect", "request", "disconnect", "closecb"}
	if len(events) != len(want) {
		t.Fatalf("events %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events %v, want %v", events, want)
		}
	}
}

func TestPortableShutdownClosesIdle(t *testing.T) {
	addr, el := portableServe(t, portableEcho)
	c := portableDial(t, addr)
	defer c.Close()
	c.Write([]byte("a"))
	c.SetReadTimeout(time.Second)
	if _, err := c.Reader().ReadBinary(1); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := el.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	// the server closed our idle connection, so the next read observes EOF
	c.SetReadTimeout(time.Second)
	if _, err := c.Reader().Next(1); err == nil {
		t.Fatal("expected error after server shutdown")
	}
	// new connections are refused
	if _, err := newStdDialer().DialConnection("tcp", addr, 300*time.Millisecond); err == nil {
		t.Fatal("dial should fail after shutdown")
	}
}

func TestPortableWriteAfterClose(t *testing.T) {
	addr, el := portableServe(t, portableEcho)
	defer el.Shutdown(context.Background())
	c := portableDial(t, addr)
	c.Close()
	if _, err := c.Write([]byte("x")); !errors.Is(err, ErrConnClosed) {
		t.Fatalf("want ErrConnClosed, got %v", err)
	}
	if err := c.Writer().Flush(); !errors.Is(err, ErrConnClosed) {
		t.Fatalf("want ErrConnClosed, got %v", err)
	}
}

func TestPortableUntil(t *testing.T) {
	addr, el := portableServe(t, portableEcho)
	defer el.Shutdown(context.Background())
	c := portableDial(t, addr)
	defer c.Close()
	c.SetReadTimeout(2 * time.Second)
	c.Write([]byte("line one\nline two\n"))
	l1, err := c.Reader().Until('\n')
	if err != nil || string(l1) != "line one\n" {
		t.Fatalf("got %q err=%v", l1, err)
	}
	l2, err := c.Reader().Until('\n')
	if err != nil || string(l2) != "line two\n" {
		t.Fatalf("got %q err=%v", l2, err)
	}
}

func TestPortableUDPUnsupported(t *testing.T) {
	if _, err := newStdListener("udp", "127.0.0.1:0"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
	if _, err := newStdDialer().DialTimeout("udp", "127.0.0.1:1", time.Second); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}
