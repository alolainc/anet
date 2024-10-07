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
	"context"
	"encoding/binary"
	"fmt"

	"github.com/alolainc/anet"
	"github.com/alolainc/anet/pkg/mux"

	"github.com/alolainc/anet/benchmarks/anet/codec"
	"github.com/alolainc/anet/benchmarks/runner"
)

func NewMuxServer() runner.Server {
	return &muxServer{}
}

var _ runner.Server = &muxServer{}

type muxServer struct{}

func (s *muxServer) Run(network, address string) error {
	// new listener
	listener, err := anet.CreateListener(network, address)
	if err != nil {
		panic(err)
	}

	// new server
	opt := anet.WithOnPrepare(s.prepare)
	eventLoop, err := anet.NewEventLoop(s.handler, opt)
	if err != nil {
		panic(err)
	}
	// start listen loop ...
	return eventLoop.Serve(listener)
}

type connkey struct{}

var ctxkey connkey

func (s *muxServer) prepare(conn anet.Connection) context.Context {
	mc := newMuxConn(conn)
	ctx := context.WithValue(context.Background(), ctxkey, mc)
	return ctx
}

func (s *muxServer) handler(ctx context.Context, conn anet.Connection) (err error) {
	mc := ctx.Value(ctxkey).(*muxConn)
	reader := conn.Reader()

	bLen, err := reader.Peek(4)
	if err != nil {
		return err
	}
	length := int(binary.BigEndian.Uint32(bLen)) + 4

	r2, err := reader.Slice(length)
	if err != nil {
		return err
	}

	// handler must use another goroutine
	go func() {
		req := &runner.Message{}
		err = codec.Decode(r2, req)
		if err != nil {
			panic(fmt.Errorf("anet decode failed: %s", err.Error()))
		}

		// handler
		resp := runner.ProcessRequest(reporter, req)

		// encode
		writer := anet.NewLinkBuffer()
		err = codec.Encode(writer, resp)
		if err != nil {
			panic(fmt.Errorf("anet encode failed: %s", err.Error()))
		}
		mc.Put(func() (buf anet.Writer, isNil bool) {
			return writer, false
		})
	}()
	return nil
}

func newMuxConn(conn anet.Connection) *muxConn {
	mc := &muxConn{}
	mc.conn = conn
	mc.wqueue = mux.NewShardQueue(mux.ShardSize, conn)
	return mc
}

type muxConn struct {
	conn   anet.Connection
	wqueue *mux.ShardQueue // use for write
}

// Put puts the buffer getter back to the queue.
func (c *muxConn) Put(gt mux.WriterGetter) {
	c.wqueue.Add(gt)
}
