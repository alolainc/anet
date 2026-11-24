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

package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestTextOutput(t *testing.T) {
	defer SetLogger(nil) // restores stderr
	var buf bytes.Buffer
	SetOutput(&buf)
	Printf("[anet]: accept conn failed: %v", "boom")
	if !strings.Contains(buf.String(), "[anet]: accept conn failed: boom") {
		t.Fatalf("got %q", buf.String())
	}
}

func TestSlogOutput(t *testing.T) {
	defer SetLogger(nil)
	var buf bytes.Buffer
	SetLogger(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	Printf("[anet]: poller close failed: %v\n", "EBADF")
	Infof("[anet]: re-accept conn success: %v", "1.2.3.4") // below the handler's level: dropped
	out := buf.String()
	for _, want := range []string{`"level":"WARN"`, `"msg":"poller close failed: EBADF"`, `"lib":"anet"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in %s", want, out)
		}
	}
	if strings.Contains(out, "re-accept") {
		t.Fatalf("info record should be filtered: %s", out)
	}
}
