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

package anet

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/alolainc/anet/internal/logging"
)

// The public API must behave the same whichever backend serves it. Run an echo through every backend.
func TestPublicAPIOnEachBackend(t *testing.T) {
	backends := map[string]backend{"portable": portableBackend{}}
	if _, ok := platform.(portableBackend); !ok {
		backends["native"] = platform
	}
	old := platform
	defer func() { platform = old }()

	for name, b := range backends {
		t.Run(name, func(t *testing.T) {
			platform = b
			ln, err := CreateListener("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			el, err := NewEventLoop(func(ctx context.Context, c Connection) error {
				msg, err := c.Reader().ReadString(c.Reader().Len())
				if err != nil {
					return err
				}
				c.Writer().WriteString(msg)
				return c.Writer().Flush()
			})
			if err != nil {
				t.Fatal(err)
			}
			go el.Serve(ln)
			defer el.Shutdown(context.Background())

			conn, err := DialConnection("tcp", ln.Addr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.Writer().WriteString("bridge")
			if err := conn.Writer().Flush(); err != nil {
				t.Fatal(err)
			}
			conn.SetReadTimeout(2 * time.Second)
			got, err := conn.Reader().ReadString(len("bridge"))
			if err != nil || got != "bridge" {
				t.Fatalf("got %q err=%v", got, err)
			}
			if _, ok := conn.(SocketTuner); !ok {
				t.Fatal("connections of every backend implement SocketTuner")
			}
		})
	}
}

func TestConfigureLogger(t *testing.T) {
	defer logging.SetLogger(nil)
	var buf bytes.Buffer
	if err := Configure(Config{Logger: slog.New(slog.NewTextHandler(&buf, nil))}); err != nil {
		t.Fatal(err)
	}
	logging.Printf("[anet]: accept conn failed: %v", "EMFILE")
	if out := buf.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, `msg="accept conn failed: EMFILE"`) {
		t.Fatalf("record not routed to Config.Logger: %q", out)
	}
}

func TestPortableIgnoresReusePort(t *testing.T) {
	ln, err := (portableBackend{}).createReusePortListener("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	el := newStdEventLoopWith(&options{onRequest: func(ctx context.Context, c Connection) error {
		msg, _ := c.Reader().ReadString(c.Reader().Len())
		c.Writer().WriteString(msg)
		return c.Writer().Flush()
	}, reusePort: true})
	go el.Serve(ln)
	defer el.Shutdown(context.Background())
	c, err := newStdDialer().DialConnection("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Writer().WriteString("z")
	c.Writer().Flush()
	c.SetReadTimeout(2 * time.Second)
	if got, err := c.Reader().ReadString(1); err != nil || got != "z" {
		t.Fatalf("got %q err=%v", got, err)
	}
}
