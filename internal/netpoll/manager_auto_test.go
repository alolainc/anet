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

package netpoll

import (
	"sync/atomic"
	"testing"
)

func TestAutoNumLoops(t *testing.T) {
	t.Run("raises the default", func(t *testing.T) {
		m := NewManager(1)
		m.AutoNumLoops(4)
		if got := atomic.LoadInt32(&m.numLoops); got != 4 {
			t.Fatalf("numLoops = %d, want 4", got)
		}
	})
	t.Run("never lowers", func(t *testing.T) {
		m := NewManager(8)
		m.AutoNumLoops(4)
		if got := atomic.LoadInt32(&m.numLoops); got != 8 {
			t.Fatalf("numLoops = %d, want 8", got)
		}
	})
	t.Run("respects an explicit choice", func(t *testing.T) {
		m := NewManager(1)
		atomic.StoreInt32(&m.explicit, 1)
		m.AutoNumLoops(4)
		if got := atomic.LoadInt32(&m.numLoops); got != 1 {
			t.Fatalf("numLoops = %d, want 1", got)
		}
	})
	t.Run("leaves running pollers alone", func(t *testing.T) {
		m := NewManager(1)
		atomic.StoreInt32(&m.status, managerInitialized)
		m.AutoNumLoops(4)
		if got := atomic.LoadInt32(&m.numLoops); got != 1 {
			t.Fatalf("numLoops = %d, want 1", got)
		}
	})
	t.Run("ignores invalid values", func(t *testing.T) {
		m := NewManager(2)
		m.AutoNumLoops(0)
		if got := atomic.LoadInt32(&m.numLoops); got != 2 {
			t.Fatalf("numLoops = %d, want 2", got)
		}
	})
}
