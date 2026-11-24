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

package anet

import (
	"context"
	"io"
	"net"
	"time"

	"github.com/alolainc/anet/internal/logging"
	"github.com/alolainc/anet/internal/runner"
)

// This file is the public entry point of anet. It is the same on every platform: each function hands the
// platform specific part to the backend (see backend.go).

// Configure tunes the internal behavior of anet. Call it in an init function: the pollers read these
// settings when they start. Fields left at their zero value keep the default.
func Configure(config Config) (err error) {
	if config.PollerNum > 0 {
		if err = platform.setNumLoops(config.PollerNum); err != nil {
			return err
		}
	}
	if config.BufferSize > 0 {
		defaultLinkBufferSize = config.BufferSize
	}
	if config.Runner != nil {
		runner.RunTask = config.Runner
	}
	if config.LoggerOutput != nil {
		logging.SetOutput(config.LoggerOutput)
	}
	if config.Logger != nil {
		logging.SetLogger(config.Logger)
	}
	if config.LoadBalance >= 0 {
		if err = platform.setLoadBalance(config.LoadBalance); err != nil {
			return err
		}
	}
	return nil
}

// Initialize starts the pollers now instead of on first use. It is safe to call it more than once.
func Initialize() { platform.initialize() }

// SetNumLoops sets the number of pollers. By default it is runtime.GOMAXPROCS(0)/20+1, which suits the
// goroutine request mode; the default inline mode raises it to GOMAXPROCS (see WithInlineRequest).
// Call it before any connection is created, for example in an init function.
//
// Deprecated: use Configure instead.
func SetNumLoops(numLoops int) error { return platform.setNumLoops(numLoops) }

// SetLoadBalance sets how connections are spread over the pollers. It only matters with more than one poller.
//
// Deprecated: use Configure instead.
func SetLoadBalance(lb LoadBalance) error { return platform.setLoadBalance(lb) }

// SetLoggerOutput sets where anet writes its text log. To get structured records instead, set Config.Logger.
//
// Deprecated: use Configure instead.
func SetLoggerOutput(w io.Writer) { logging.SetOutput(w) }

// SetRunner sets the function that runs OnRequest and OnConnect in the goroutine request mode.
//
// Deprecated: use Configure and set Config.Runner instead.
func SetRunner(f func(ctx context.Context, f func())) { runner.RunTask = f }

// DisableGopool runs OnRequest with a plain `go` statement instead of the goroutine pool. Reusing
// goroutines avoids stack growth when handlers use a lot of stack; without that, plain goroutines are cheaper.
//
// Deprecated: use Configure and set Config.Runner instead.
func DisableGopool() error {
	runner.UseGoRunTask()
	return nil
}

// NewEventLoop creates a server that calls onRequest when a connection has data to read.
// See the options (With...) for the other callbacks and for how handlers run.
func NewEventLoop(onRequest OnRequest, ops ...Option) (EventLoop, error) {
	opts := &options{
		onRequest:  onRequest,
		inline:     true, // default; WithGoroutineRequest opts out
		inlineAuto: true,
	}
	for _, do := range ops {
		do.f(opts)
	}
	return platform.newEventLoop(opts), nil
}

// NewDialer returns a Dialer for TCP and unix sockets.
func NewDialer() Dialer { return platform.dialer() }

// DialConnection dials address and returns a Connection.
func DialConnection(network, address string, timeout time.Duration) (connection Connection, err error) {
	return platform.dialer().DialConnection(network, address, timeout)
}

// CreateListener listens on addr (TCP or unix socket; UDP is not supported).
func CreateListener(network, addr string) (l Listener, err error) {
	return platform.createListener(network, addr)
}

// CreateReusePortListener listens on a TCP address for an EventLoop created WithReusePort: with SO_REUSEPORT
// where the kernel balances such sockets (Linux, FreeBSD), as a plain listener elsewhere.
func CreateReusePortListener(network, addr string) (Listener, error) {
	return platform.createReusePortListener(network, addr)
}

// ConvertListener wraps a net.Listener so that an EventLoop can serve it.
func ConvertListener(l net.Listener) (nl Listener, err error) { return platform.convertListener(l) }
