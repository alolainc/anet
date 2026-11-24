// Copyright 2024 Alola Inc.
// Copyright 2022 CloudWeGo Authors
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

package netpoll

import (
	. "github.com/alolainc/anet/internal/testutil"

	"runtime"
	"sync"
	"testing"
)

func TestPollManagerReset(t *testing.T) {
	n := Default.numLoops
	err := Default.Reset()
	MustNil(t, err)
	Equal(t, len(Default.polls), int(n))
}

func TestPollManagerSetNumLoops(t *testing.T) {
	pm := NewManager(1)

	startGs := runtime.NumGoroutine()
	poll := pm.Pick()
	newGs := runtime.NumGoroutine()
	Assert(t, poll != nil)
	t.Logf("old=%d, new=%d", startGs, newGs)
	// FIXME: it's unstable due to background goroutines created by other tests
	// Assert(t, newGs-startGs == 1)

	// change pollers
	oldGs := newGs
	err := pm.SetNumLoops(100)
	MustNil(t, err)
	newGs = runtime.NumGoroutine()
	t.Logf("old=%d, new=%d", oldGs, newGs)
	// Assert(t, newGs == oldGs)

	// trigger polls adjustment
	var wg sync.WaitGroup
	finish := make(chan struct{})
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			poll := pm.Pick()
			Assert(t, poll != nil)
			Assert(t, len(pm.polls) == 100)
			wg.Done()
			<-finish // hold goroutines
		}()
	}
	wg.Wait()
	close(finish)
}
