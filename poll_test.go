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

//go:build !windows

package anet

import (
	"runtime"
	"testing"
)

// TestPollManager checks that two connections over a socket pair exchange data through the pollers.
func TestPollManager(t *testing.T) {
	r, w := GetSysFdPairs()
	rconn, wconn := &connection{}, &connection{}
	err := rconn.init(&netFD{fd: r}, nil)
	MustNil(t, err)
	err = wconn.init(&netFD{fd: w}, nil)
	MustNil(t, err)

	msg := []byte("hello world")
	n, err := wconn.Write(msg)
	MustNil(t, err)
	Equal(t, n, len(msg))

	p, err := rconn.Reader().Next(n)
	MustNil(t, err)
	Equal(t, string(p), string(msg))

	err = wconn.Close()
	MustNil(t, err)
	for rconn.IsActive() || wconn.IsActive() {
		runtime.Gosched()
	}
}
