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

	"github.com/panjf2000/gnet/v2"
	"github.com/panjf2000/gnet/v2/pkg/pool/goroutine"

	"github.com/alolainc/anet/benchmarks/gnet_v2/codec"
	"github.com/alolainc/anet/benchmarks/runner"
)

func NewRPCServer() runner.Server {
	return &rpcServer{}
}

var _ runner.Server = &rpcServer{}

type rpcServer struct {
	gnet.BuiltinEventEngine
	pool *goroutine.Pool
}

func (s *rpcServer) Run(network, address string) error {
	p := goroutine.Default()
	defer p.Release()

	echo := &rpcServer{pool: p}
	return gnet.Run(echo, fmt.Sprintf("%s://%s", network, address), gnet.WithMulticore(true))
}

func (s *rpcServer) OnTraffic(c gnet.Conn) (action gnet.Action) {
	conner := codec.GetOrNewConner(c)
	// Next returns a slice that is only valid until OnTraffic returns; Decode copies it.
	frame, err := c.Next(-1)
	if err != nil {
		return gnet.Close
	}
	req, err := conner.Decode(frame)
	if err != nil {
		return gnet.Close
	}
	if req == nil {
		return gnet.None
	}

	// Use the pool to unblock the event-loop.
	s.pool.Submit(func() {
		// handler
		resp := runner.ProcessRequest(reporter, req)

		// encode
		out := conner.Encode(resp)
		if err := c.AsyncWrite(out, nil); err != nil {
			panic(err)
		}
	})
	return gnet.None
}

func (s *rpcServer) OnClose(c gnet.Conn, err error) (action gnet.Action) {
	codec.RemoveConner(c)
	return gnet.None
}
