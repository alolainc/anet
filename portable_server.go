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
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// stdListener adapts a net.Listener to Listener on platforms without file descriptors.
type stdListener struct {
	net.Listener
}

// Fd implements Listener. The portable backend does not use file descriptors.
func (stdListener) Fd() int { return -1 }

func newStdListener(network, addr string) (Listener, error) {
	if network == "udp" || network == "udp4" || network == "udp6" {
		return nil, Exception(ErrUnsupported, "UDP")
	}
	ln, err := net.Listen(network, addr)
	if err != nil {
		return nil, err
	}
	return convertStdListener(ln), nil
}

func convertStdListener(l net.Listener) Listener {
	if ln, ok := l.(Listener); ok {
		return ln
	}
	return stdListener{l}
}

// stdDialer dials connections served by the portable backend.
type stdDialer struct{}

func newStdDialer() Dialer { return stdDialer{} }

// DialConnection implements Dialer.
func (stdDialer) DialConnection(network, address string, timeout time.Duration) (Connection, error) {
	raw, err := dialStd(network, address, timeout)
	if err != nil {
		return nil, err
	}
	return newStdConn(raw, nil), nil
}

// DialTimeout implements Dialer.
func (stdDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	raw, err := dialStd(network, address, timeout)
	if err != nil {
		return nil, err
	}
	return newStdConn(raw, nil), nil
}

func dialStd(network, address string, timeout time.Duration) (net.Conn, error) {
	switch network {
	case "udp", "udp4", "udp6":
		return nil, Exception(ErrUnsupported, "UDP")
	}
	d := net.Dialer{Timeout: timeout}
	return d.Dial(network, address)
}

// stdEventLoop is the portable EventLoop: one accept loop, one read goroutine per connection.
type stdEventLoop struct {
	mu    sync.Mutex
	opts  *options
	ln    net.Listener
	conns sync.Map // *stdConn -> struct{}
	stop  chan error
	done  int32 // set once Shutdown was called
}

func newStdEventLoop(onRequest OnRequest, ops ...Option) EventLoop {
	opts := &options{onRequest: onRequest}
	for _, do := range ops {
		do.f(opts)
	}
	return newStdEventLoopWith(opts)
}

func newStdEventLoopWith(opts *options) EventLoop {
	return &stdEventLoop{opts: opts, stop: make(chan error, 1)}
}

// Serve implements EventLoop.
func (e *stdEventLoop) Serve(ln net.Listener) error {
	e.mu.Lock()
	e.ln = ln
	e.mu.Unlock()
	go e.acceptLoop(ln)
	return <-e.stop
}

func (e *stdEventLoop) quit(err error) {
	select {
	case e.stop <- err:
	default:
	}
}

func (e *stdEventLoop) acceptLoop(ln net.Listener) {
	var delay time.Duration
	for {
		raw, err := ln.Accept()
		if err != nil {
			if atomic.LoadInt32(&e.done) == 1 || errors.Is(err, net.ErrClosed) {
				e.quit(nil)
				return
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() { // temporary failure, e.g. out of file handles
				if delay == 0 {
					delay = 5 * time.Millisecond
				} else if delay *= 2; delay > time.Second {
					delay = time.Second
				}
				time.Sleep(delay)
				continue
			}
			e.quit(err)
			return
		}
		delay = 0
		e.onAccept(raw)
	}
}

func (e *stdEventLoop) onAccept(raw net.Conn) {
	c := newStdConn(raw, e.opts)
	e.conns.Store(c, struct{}{})
	c.AddCloseCallback(func(Connection) error {
		e.conns.Delete(c)
		return nil
	})
}

// Shutdown implements EventLoop. It stops accepting and closes idle connections until none are left or ctx ends.
func (e *stdEventLoop) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	ln := e.ln
	e.ln = nil
	e.mu.Unlock()
	if ln == nil {
		return nil
	}
	atomic.StoreInt32(&e.done, 1)
	ln.Close()
	e.quit(nil)

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		has := false
		e.conns.Range(func(k, _ any) bool {
			c := k.(*stdConn)
			if c.isIdle() {
				c.Close()
			}
			has = true
			return true
		})
		if !has {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
