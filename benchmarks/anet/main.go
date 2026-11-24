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
	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"

	"github.com/alolainc/anet"

	"github.com/alolainc/anet/benchmarks/runner"
	"github.com/alolainc/anet/benchmarks/runner/perf"
	"github.com/alolainc/anet/benchmarks/runner/svr"
)

func main() {
	svr.Serve(NewServer)
}

var reporter = perf.NewRecorder("ANET@Server")

// init tunes anet from the environment so that benchmark runs can compare configurations:
//
//	ANET_LOOPS=<n>          number of pollers (default: anet's own default)
//	ANET_RUNNER=go|inline   how OnRequest is run: a new goroutine (default) or inline on the poller
//	ANET_PPROF=<addr>       serve net/http/pprof on addr for profiling
func init() {
	if addr := os.Getenv("ANET_PPROF"); addr != "" {
		go http.ListenAndServe(addr, nil)
	}
	if v := os.Getenv("ANET_LOOPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			anet.SetNumLoops(n)
		}
	}
	switch os.Getenv("ANET_RUNNER") {
	case "inline":
		anet.SetRunner(func(ctx context.Context, f func()) { f() })
	default:
		anet.DisableGopool()
	}
}

func NewServer(mode runner.Mode) runner.Server {
	switch mode {
	case runner.Mode_Echo, runner.Mode_Idle:
		return NewRPCServer()
	case runner.Mode_Mux:
		return NewMuxServer()
	}
	return nil
}
