/*
 * Copyright 2024 Alola Inc.
 * Copyright 2025 CloudWeGo Authors
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

package runner

import (
	"context"

	"github.com/alolainc/gopool"
)

// RunTask runs the `f` in background, and `ctx` is optional.
// `ctx` is used to pass to underlying implementation
var RunTask func(ctx context.Context, f func()) = poolRunTask

// poolRunTask submits f to alolainc/gopool. If the pool rejects the task
// (e.g. it is released), f still runs on a fresh goroutine so it is never dropped.
func poolRunTask(ctx context.Context, f func()) {
	if err := gopool.Submit(ctx, f); err != nil {
		go f()
	}
}

func goRunTask(ctx context.Context, f func()) {
	go f()
}

// UseGoRunTask updates RunTask with goRunTask which creates
// a new goroutine for the given func, basically `go f()`
func UseGoRunTask() {
	RunTask = goRunTask
}
