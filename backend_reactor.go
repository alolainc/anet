// Copyright 2024 Alola Inc.
// Copyright 2022 CloudWeGo Authors
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
	"errors"
	"fmt"
	"net"
	"runtime"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/alolainc/anet/internal/netpoll"
	"github.com/alolainc/anet/internal/socket"
)

var platform backend = reactorBackend{}

// reactorBackend serves connections with the pollers of internal/netpoll.
type reactorBackend struct{}

func (reactorBackend) initialize() { _ = netpoll.Default.Pick() } // the first Pick starts the pollers

func (reactorBackend) setNumLoops(n int) error {
	if err := netpoll.Default.SetNumLoops(n); err != nil {
		return err
	}
	netpoll.Default.SetExplicit()
	return nil
}

func (reactorBackend) setLoadBalance(lb LoadBalance) error { return netpoll.Default.SetLoadBalance(lb) }

func (reactorBackend) newEventLoop(opts *options) EventLoop {
	if opts.inlineOn() {
		// Inline handlers run on the pollers, so they need about one poller per CPU. Respect an explicit choice.
		netpoll.Default.AutoNumLoops(runtime.GOMAXPROCS(0))
	}
	return &eventLoop{opts: opts, stop: make(chan error, 1)}
}

func (reactorBackend) dialer() Dialer { return reactorDialer }

func (reactorBackend) createListener(network, addr string) (Listener, error) {
	return createReactorListener(network, addr)
}

func (reactorBackend) createReusePortListener(network, addr string) (Listener, error) {
	return createReactorReusePortListener(network, addr)
}

func (reactorBackend) convertListener(l net.Listener) (Listener, error) {
	return convertReactorListener(l)
}

func (reactorBackend) newPacketLoop(onPacket OnPacket) PacketLoop {
	return newReactorPacketLoop(onPacket)
}

type eventLoop struct {
	sync.Mutex
	opts *options
	svrs []*server // one per listener: more than one WithReusePort
	stop chan error
}

// Serve implements EventLoop.
func (evl *eventLoop) Serve(ln net.Listener) error {
	npln, err := ConvertListener(ln)
	if err != nil {
		return err
	}
	lns := []Listener{npln}
	if evl.opts.reusePort {
		if lns, err = acceptListeners(npln, netpoll.Default.NumLoops()); err != nil {
			return err
		}
	}
	evl.Lock()
	for _, l := range lns {
		svr := newServer(l, evl.opts, evl.quit)
		evl.svrs = append(evl.svrs, svr)
		svr.Run() // each server registers with the next poller (round robin), so accepts are spread too
	}
	evl.Unlock()

	err = evl.waitQuit()
	// ensure evl will not be finalized until Serve returns
	runtime.SetFinalizer(evl, nil)
	return err
}

// reusePortBalances is socket.ReusePortBalances; tests switch it to exercise the shared-listener mode.
var reusePortBalances = socket.ReusePortBalances

// acceptListeners returns one listener per poller for WithReusePort. Where the kernel balances SO_REUSEPORT
// sockets, they are new listeners on the address of ln (which must have SO_REUSEPORT); elsewhere every poller
// gets ln itself: each one is woken for a new connection and the first accept wins.
func acceptListeners(ln Listener, n int) ([]Listener, error) {
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return nil, Exception(ErrUnsupported, "WithReusePort needs a TCP listener")
	}
	lns := []Listener{ln}
	if !reusePortBalances {
		for len(lns) < n {
			lns = append(lns, ln)
		}
		return lns, nil
	}
	network := "tcp4"
	if addr.IP.To4() == nil {
		network = "tcp6"
	}
	for len(lns) < n {
		l, err := CreateReusePortListener(network, addr.String())
		if err != nil {
			for _, l := range lns[1:] {
				l.Close()
			}
			if errors.Is(err, unix.EADDRINUSE) {
				err = fmt.Errorf("WithReusePort: the listener passed to Serve must come from CreateReusePortListener: %w", err)
			}
			return nil, err
		}
		lns = append(lns, l)
	}
	return lns, nil
}

// Shutdown signals a shutdown a begins server closing.
func (evl *eventLoop) Shutdown(ctx context.Context) error {
	evl.Lock()
	svrs := evl.svrs
	evl.svrs = nil
	evl.Unlock()

	if len(svrs) == 0 {
		return nil
	}
	evl.quit(nil)
	// Detach every listener from its poller first, then close each distinct listener once: a listener shared
	// by several pollers must not be closed (and its descriptor reused) while another poller still watches it.
	for _, svr := range svrs {
		svr.stopAccepting()
	}
	closed := map[Listener]bool{}
	for _, svr := range svrs {
		if !closed[svr.ln] {
			closed[svr.ln] = true
			svr.ln.Close()
		}
	}
	var first error
	for _, svr := range svrs {
		if err := svr.drain(ctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// waitQuit waits for a quit signal
func (evl *eventLoop) waitQuit() error {
	return <-evl.stop
}

func (evl *eventLoop) quit(err error) {
	select {
	case evl.stop <- err:
	default:
	}
}
