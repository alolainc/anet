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

	"github.com/alolainc/anet/internal/netpoll"
)

func TestExplicitSetNumLoops(t *testing.T) {
	old := netpoll.Default
	defer func() { netpoll.Default = old }()
	netpoll.Default = netpoll.NewManager(1)

	if err := SetNumLoops(3); err != nil {
		t.Fatal(err)
	}
	if !netpoll.Default.Explicit() {
		t.Fatal("SetNumLoops must mark the choice as explicit")
	}
	if _, err := NewEventLoop(nil, WithInlineRequest()); err != nil {
		t.Fatal(err)
	}
	if got := netpoll.Default.NumLoops(); got != 3 {
		t.Fatalf("explicit numLoops overridden: %d", got)
	}
}

func TestInlineEventLoopAutoNumLoops(t *testing.T) {
	old := netpoll.Default
	defer func() { netpoll.Default = old }()
	netpoll.Default = netpoll.NewManager(1)

	if _, err := NewEventLoop(nil, WithGoroutineRequest()); err != nil { // without inline: untouched
		t.Fatal(err)
	}
	if got := netpoll.Default.NumLoops(); got != 1 {
		t.Fatalf("non-inline loop changed numLoops to %d", got)
	}
	if _, err := NewEventLoop(nil, WithInlineRequest()); err != nil {
		t.Fatal(err)
	}
	want := runtime.GOMAXPROCS(0)
	if want < 1 {
		want = 1
	}
	got := netpoll.Default.NumLoops()
	t.Logf("GOMAXPROCS=%d numLoops=%d", runtime.GOMAXPROCS(0), got)
	if got < want {
		t.Fatalf("inline loop numLoops = %d, want >= %d", got, want)
	}
}
