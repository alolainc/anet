/*
 * Copyright 2024 Alola Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"fmt"
	"sync"

	"github.com/panjf2000/gnet/v2"
	"github.com/panjf2000/gnet/v2/pkg/pool/goroutine"

	"github.com/alolainc/anet/benchmarks/gnet_v2/codec"
	"github.com/alolainc/anet/benchmarks/runner"
)

func NewMuxServer() runner.Server {
	return &muxServer{}
}

var _ runner.Server = &muxServer{}

type muxServer struct {
	gnet.BuiltinEventEngine
	pool *goroutine.Pool
}

func (s *muxServer) Run(network, address string) error {
	p := goroutine.Default()
	defer p.Release()

	echo := &muxServer{pool: p}
	return gnet.Run(echo, fmt.Sprintf("%s://%s", network, address), gnet.WithMulticore(true))
}

func (s *muxServer) OnTraffic(c gnet.Conn) (action gnet.Action) {
	mux := getOrNewMuxConn(c)
	// Next returns a slice that is only valid until OnTraffic returns; Decode copies it.
	frame, err := c.Next(-1)
	if err != nil {
		return gnet.Close
	}
	for {
		req, err := mux.conner.Decode(frame)
		if err != nil {
			return gnet.Close
		}
		if req == nil {
			return gnet.None
		}
		frame = nil

		// Use the pool to unblock the event-loop.
		s.pool.Submit(func() {
			// handler
			resp := runner.ProcessRequest(reporter, req)
			// encode
			select {
			case mux.wch <- resp:
			case <-mux.done:
			}
		})
	}
}

func (s *muxServer) OnClose(c gnet.Conn, err error) (action gnet.Action) {
	if v, ok := muxconnmap.LoadAndDelete(c); ok {
		close(v.(*muxConn).done)
	}
	codec.RemoveConner(c)
	return gnet.None
}

var muxconnmap sync.Map

func getOrNewMuxConn(conn gnet.Conn) *muxConn {
	c, loaded := muxconnmap.LoadOrStore(conn, &muxConn{})
	mux := c.(*muxConn)
	if loaded {
		return mux
	}
	// init
	mux.conn = conn
	mux.conner = codec.GetOrNewConner(conn)
	mux.wch = make(chan *runner.Message)
	mux.done = make(chan struct{})
	go mux.loopWrite()
	return mux
}

type muxConn struct {
	conn   gnet.Conn
	conner *codec.Conner
	wch    chan *runner.Message
	done   chan struct{} // closed when the connection is closed
}

func (mux *muxConn) loopWrite() {
	for {
		select {
		case msg := <-mux.wch:
			// encode
			out := mux.conner.Encode(msg)
			if err := mux.conn.AsyncWrite(out, nil); err != nil {
				return // connection already closed
			}
		case <-mux.done:
			return
		}
	}
}
