/*
 * Copyright 2021 CloudWeGo
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
	"fmt"
	"time"

	"github.com/alolainc/anet"
)

func main() {
	network, address := "tcp", ":8080"
	listener, _ := anet.CreateListener(network, address)

	eventLoop, _ := anet.NewEventLoop(
		handle,
		anet.WithOnPrepare(prepare),
		anet.WithOnConnect(connect),
		anet.WithReadTimeout(time.Second),
	)

	// start listen loop ...
	eventLoop.Serve(listener)
}

var _ anet.OnPrepare = prepare
var _ anet.OnConnect = connect
var _ anet.OnRequest = handle
var _ anet.CloseCallback = close

func prepare(connection anet.Connection) context.Context {
	return context.Background()
}

func close(connection anet.Connection) error {
	fmt.Printf("[%v] connection closed\n", connection.RemoteAddr())
	return nil
}

func connect(ctx context.Context, connection anet.Connection) context.Context {
	fmt.Printf("[%v] connection established\n", connection.RemoteAddr())
	connection.AddCloseCallback(close)
	return ctx
}

func handle(ctx context.Context, connection anet.Connection) error {
	reader, writer := connection.Reader(), connection.Writer()
	defer reader.Release()

	msg, _ := reader.ReadString(reader.Len())
	fmt.Printf("[recv msg] %v\n", msg)

	writer.WriteString(msg)
	writer.Flush()

	return nil
}
