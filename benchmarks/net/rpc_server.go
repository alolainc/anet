/*
 * Copyright 2024 Alola Inc.
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
	"net"
	"strings"
	"time"

	"github.com/alolainc/anet/benchmarks/net/codec"
	"github.com/alolainc/anet/benchmarks/runner"
)

func NewRPCServer() runner.Server {
	return &rpcServer{}
}

var _ runner.Server = &rpcServer{}

type rpcServer struct{}

func (s *rpcServer) Run(network, address string) error {
	// new listener
	listener, _ := net.Listen(network, address)
	for {
		_conn, err := listener.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "closed") {
				return err
			}
			time.Sleep(10 * time.Millisecond) // too many open files ?
			continue
		}

		go func(conn net.Conn) {
			conner := codec.NewConner(conn)
			defer codec.PutConner(conner)

			var err error
			for err == nil {
				err = s.handler(conner)
			}
			conn.Close()
		}(_conn)
	}
}

func (s *rpcServer) handler(conner *codec.Conner) (err error) {
	// decode
	req := &runner.Message{}
	err = conner.Decode(req)
	if err != nil {
		return err
	}

	// handler
	resp := runner.ProcessRequest(reporter, req)

	// encode
	err = conner.Encode(resp)
	if err != nil {
		return err
	}
	return nil
}
