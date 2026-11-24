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

// Package anet is a network library for Go servers and clients that need many connections at low cost.
//
// Instead of one goroutine per connection, a few pollers (epoll on Linux, kqueue on BSD and macOS) watch all
// connections. When data arrives, the poller reads it into the connection's buffer and calls the OnRequest
// handler. Idle connections cost almost nothing, and the buffers can be read and written without copying.
//
// # Server
//
//	ln, _ := anet.CreateListener("tcp", ":8080")
//	el, _ := anet.NewEventLoop(func(ctx context.Context, c anet.Connection) error {
//		msg, _ := c.Reader().ReadString(c.Reader().Len())
//		c.Writer().WriteString(msg)
//		return c.Writer().Flush()
//	})
//	el.Serve(ln)
//
// # Client
//
//	conn, err := anet.DialConnection("tcp", "127.0.0.1:8080", time.Second)
//
// # Handlers run on the poller
//
// By default OnRequest runs directly on the poller goroutine, which avoids a goroutine hand-off per request.
// The handler therefore must not block: Reader methods return ErrNotEnoughData instead of waiting for data,
// and Flush does not wait for a slow peer. If your handler waits for data, calls other services or takes
// contended locks, pass WithGoroutineRequest. The default is also switched off when WithOnConnect is set,
// because OnConnect often waits for data. See WithInlineRequest for the details.
//
// # Reading and writing
//
// Connection.Reader and Connection.Writer give nocopy access to the buffers. Data returned by Next, Peek and
// Slice stays valid until Release. See Reader and Writer.
//
// # UDP
//
// NewPacketLoop serves a *net.UDPConn (unicast, broadcast or multicast) and calls OnPacket for every datagram.
//
// # Tuning
//
// Configure sets the poller count, buffer sizes and the logger (Config.Logger takes a *slog.Logger).
// WithReusePort accepts connections on every poller. SocketTuner exposes SO_SNDBUF, SO_RCVBUF and
// TCP_NOTSENT_LOWAT, which keep latency low when the network is slow. Subpackage mux multiplexes requests over
// one connection, and subpackage tee sends one stream of frames to many connections.
//
// # Platforms
//
// Linux and the BSDs including macOS use the pollers. On other platforms (Windows) a portable backend built on
// package net provides the same API with one goroutine per connection.
package anet
