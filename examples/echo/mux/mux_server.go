/*
 * Copyright 2021 CloudWeGo Authors
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
	"time"

	"github.com/alolainc/anet"
	"github.com/alolainc/anet/pkg/mux"

	"github.com/alolainc/anet/examples/echo/codec"
)

func main() {
	network, address := "tcp", "127.0.0.1:8080"
	listener, _ := anet.CreateListener(network, address)

	eventLoop, _ := anet.NewEventLoop(
		handle,
		anet.WithOnPrepare(prepare),
		anet.WithReadTimeout(time.Second),
	)

	// start listen loop ...
	eventLoop.Serve(listener)
}

var _ anet.OnPrepare = prepare
var _ anet.OnRequest = handle

type connkey struct{}

var ctxkey connkey

func prepare(conn anet.Connection) context.Context {
	mc := newSvrMuxConn(conn)
	ctx := context.WithValue(context.Background(), ctxkey, mc)
	return ctx
}

func handle(ctx context.Context, conn anet.Connection) (err error) {
	mc := ctx.Value(ctxkey).(*svrMuxConn)
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
		req := &codec.Message{}
		err = codec.Decode(r2, req)
		if err != nil {
			panic(fmt.Errorf("anet decode failed: %s", err.Error()))
		}

		// handler
		resp := req

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

func newSvrMuxConn(conn anet.Connection) *svrMuxConn {
	mc := &svrMuxConn{}
	mc.conn = conn
	mc.wqueue = mux.NewShardQueue(mux.ShardSize, conn)
	return mc
}

type svrMuxConn struct {
	conn   anet.Connection
	wqueue *mux.ShardQueue // use for write
}

// Put puts the buffer getter back to the queue.
func (c *svrMuxConn) Put(gt mux.WriterGetter) {
	c.wqueue.Add(gt)
}
