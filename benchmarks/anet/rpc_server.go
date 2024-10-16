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

	"github.com/alolainc/anet"

	"github.com/alolainc/anet/benchmarks/anet/codec"
	"github.com/alolainc/anet/benchmarks/runner"
)

func NewRPCServer() runner.Server {
	return &rpcServer{}
}

var _ runner.Server = &rpcServer{}

type rpcServer struct{}

func (s *rpcServer) Run(network, address string) error {
	// new listener
	listener, err := anet.CreateListener(network, address)
	if err != nil {
		panic(err)
	}

	// new server
	eventLoop, err := anet.NewEventLoop(s.handler)
	if err != nil {
		panic(err)
	}
	// start listen loop ...
	return eventLoop.Serve(listener)
}

func (s *rpcServer) handler(ctx context.Context, conn anet.Connection) (err error) {
	reader, writer := conn.Reader(), conn.Writer()

	// decode
	req := &runner.Message{}
	err = codec.Decode(reader, req)
	if err != nil {
		return err
	}

	// handler
	resp := runner.ProcessRequest(reporter, req)

	// encode
	err = codec.Encode(writer, resp)
	if err != nil {
		return err
	}
	return nil
}
