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
	"time"

	"github.com/alolainc/anet"

	"github.com/alolainc/anet/benchmarks/anet/codec"
	"github.com/alolainc/anet/benchmarks/runner"
	"github.com/alolainc/anet/benchmarks/runner/connpool"
)

func NewClientWithConnpool(network, address string) runner.Client {
	return &poolclient{
		network:  network,
		address:  address,
		connpool: connpool.NewLongPool(1024),
	}
}

var _ runner.Client = &poolclient{}

type poolclient struct {
	network  string
	address  string
	connpool connpool.ConnPool
}

func (cli *poolclient) DialTimeout(network, address string, timeout time.Duration) (runner.Conn, error) {
	return anet.DialConnection(network, address, timeout)
}

func (cli *poolclient) Echo(req *runner.Message) (resp *runner.Message, err error) {
	// get conn & codec
	cn, err := cli.connpool.Get(cli.network, cli.address, cli, time.Second)
	if err != nil {
		return nil, err
	}
	defer func() {
		cli.connpool.Put(cn, err)
	}()
	conn := cn.(anet.Connection)
	reader, writer := conn.Reader(), conn.Writer()

	// encode
	err = codec.Encode(writer, req)
	if err != nil {
		return nil, err
	}

	// decode
	resp = &runner.Message{}
	err = codec.Decode(reader, resp)
	if err != nil {
		return nil, err
	}

	// reporter
	runner.ProcessResponse(resp)
	return resp, nil
}
