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

// Package logging is the logger shared by the packages of anet. It writes either to a standard log.Logger
// (the default, and what Config.LoggerOutput selects) or to a *slog.Logger (Config.Logger).
package logging

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
)

type sink struct {
	std *log.Logger  // used when sl is nil
	sl  *slog.Logger // structured logger chosen by the user
}

var cur atomic.Pointer[sink]

func init() { SetOutput(os.Stderr) }

// SetOutput writes the log as text lines to w.
func SetOutput(w io.Writer) { cur.Store(&sink{std: log.New(w, "", log.LstdFlags)}) }

// SetLogger sends the log to l. A nil l restores the default text log on stderr.
func SetLogger(l *slog.Logger) {
	if l == nil {
		SetOutput(os.Stderr)
		return
	}
	cur.Store(&sink{sl: l})
}

// Printf logs a problem anet recovered from or reports to the caller: a warning.
func Printf(format string, v ...any) { logAt(slog.LevelWarn, format, v...) }

// Infof logs a normal event worth knowing about.
func Infof(format string, v ...any) { logAt(slog.LevelInfo, format, v...) }

func logAt(level slog.Level, format string, v ...any) {
	s := cur.Load()
	if s.sl == nil {
		s.std.Printf(format, v...)
		return
	}
	if !s.sl.Enabled(context.Background(), level) {
		return
	}
	// slog records carry their own source; drop the text prefix and the trailing newline some messages have
	msg := strings.TrimSpace(strings.TrimPrefix(fmt.Sprintf(format, v...), "[anet]: "))
	s.sl.Log(context.Background(), level, msg, slog.String("lib", "anet"))
}
