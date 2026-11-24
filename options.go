// Copyright 2024 Alola Inc.
// Copyright 2024 CloudWeGo Authors
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

import "time"

// Option .
type Option struct {
	f func(*options)
}

type options struct {
	onPrepare    OnPrepare
	onConnect    OnConnect
	onDisconnect OnDisconnect
	onRequest    OnRequest
	readTimeout  time.Duration
	writeTimeout time.Duration
	idleTimeout  time.Duration
	inline       bool // run OnRequest on the poller; on by default for EventLoop
	inlineAuto   bool // inline is the default (not chosen with WithInlineRequest): skipped when OnConnect is set
	sendBuffer   int  // SO_SNDBUF, 0 = system default
	recvBuffer   int  // SO_RCVBUF, 0 = system default
	notSentLowat int  // TCP_NOTSENT_LOWAT, 0 = system default
	reusePort    bool // Serve opens one SO_REUSEPORT listener per poller
}

// WithOnPrepare registers the OnPrepare method to EventLoop.
func WithOnPrepare(onPrepare OnPrepare) Option {
	return Option{func(op *options) {
		op.onPrepare = onPrepare
	}}
}

// WithOnConnect registers the OnConnect method to EventLoop.
func WithOnConnect(onConnect OnConnect) Option {
	return Option{func(op *options) {
		op.onConnect = onConnect
	}}
}

// WithOnDisconnect registers the OnDisconnect method to EventLoop.
func WithOnDisconnect(onDisconnect OnDisconnect) Option {
	return Option{func(op *options) {
		op.onDisconnect = onDisconnect
	}}
}

// WithReadTimeout sets the read timeout of connections.
func WithReadTimeout(timeout time.Duration) Option {
	return Option{func(op *options) {
		op.readTimeout = timeout
	}}
}

// WithWriteTimeout sets the write timeout of connections.
func WithWriteTimeout(timeout time.Duration) Option {
	return Option{func(op *options) {
		op.writeTimeout = timeout
	}}
}

// WithInlineRequest makes OnRequest run directly on the poller goroutine instead of a separate goroutine.
// It is already the default for an EventLoop, except when WithOnConnect is set; pass it explicitly to keep
// inline mode together with OnConnect (which then must not block either), or use WithGoroutineRequest to turn it off.
// This removes the goroutine hand-off and wake-up cost per request, which helps fast handlers.
//
// The handler MUST NOT block, because it runs on the goroutine that reads the socket:
//   - Reader methods never wait for data. If the buffered data is insufficient they return
//     ErrNotEnoughData without consuming anything, and the handler should return nil to wait for more.
//     Check Reader().Len() or use Peek before consuming, so a partial message is not half-read.
//   - OnRequest is called again each time new data arrives while unread data remains, and is not
//     called in a loop when it returns without consuming anything.
//   - Writer().Flush() does not wait for a slow peer: data that cannot be sent immediately is queued
//     and sent by the poller.
//
// It applies only on the server side (EventLoop), and suits handlers that are CPU-light and never wait on I/O.
//
// All handlers and their writes now run on the pollers, so the number of pollers bounds the parallelism.
// NewEventLoop therefore sets the number of pollers to runtime.GOMAXPROCS(0) automatically, unless you chose
// it with SetNumLoops or Config.PollerNum, or the pollers are already running. With fewer pollers than CPUs
// it is much SLOWER than the default mode.
func WithInlineRequest() Option {
	return Option{func(op *options) {
		op.inline = true
		op.inlineAuto = false
	}}
}

// WithGoroutineRequest runs each OnRequest call on a goroutine instead of on the poller, which turns off the
// default inline mode (see WithInlineRequest). Use it when the handler may block: wait for data with
// Reader().Next/Peek/ReadBinary, call another service, take a contended lock, and so on.
// Reader methods then wait for the data to arrive, as in netpoll.
func WithGoroutineRequest() Option {
	return Option{func(op *options) {
		op.inline = false
		op.inlineAuto = false
	}}
}

// WithSendBuffer sets SO_SNDBUF on every accepted connection. See SocketTuner.SetSendBuffer.
func WithSendBuffer(n int) Option {
	return Option{func(op *options) { op.sendBuffer = n }}
}

// WithRecvBuffer sets SO_RCVBUF on every accepted connection. See SocketTuner.SetRecvBuffer.
func WithRecvBuffer(n int) Option {
	return Option{func(op *options) { op.recvBuffer = n }}
}

// WithNotSentLowat sets TCP_NOTSENT_LOWAT on every accepted connection, which keeps the kernel send queue
// short so that slow networks add less latency. See SocketTuner.SetNotSentLowat.
func WithNotSentLowat(n int) Option {
	return Option{func(op *options) { op.notSentLowat = n }}
}

// WithReusePort makes Serve accept on every poller instead of one, which helps servers that accept many
// short-lived connections; long-lived connections gain little. Create the listener with CreateReusePortListener.
//
// On Linux (SO_REUSEPORT) and FreeBSD (SO_REUSEPORT_LB), Serve opens one more listener per poller on the same
// address and the kernel spreads new connections over them; a listener without SO_REUSEPORT makes Serve
// return an error. On macOS and the other BSDs, where the kernel does not balance SO_REUSEPORT sockets, every
// poller watches the one listener and the first to accept a connection takes it. On the portable backend
// (Windows) the option has no effect.
func WithReusePort() Option {
	return Option{func(op *options) { op.reusePort = true }}
}

// WithIdleTimeout sets the idle timeout of connections.
func WithIdleTimeout(timeout time.Duration) Option {
	return Option{func(op *options) {
		op.idleTimeout = timeout
	}}
}

// inlineOn reports whether OnRequest handlers run on the poller. The default (inlineAuto) is skipped when an
// OnConnect callback is set, because OnConnect commonly waits for data and must not block a poller.
func (o *options) inlineOn() bool {
	return o.inline && !(o.inlineAuto && o.onConnect != nil)
}
