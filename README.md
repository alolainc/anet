# anet

A Go network library for servers that hold many connections. A few pollers (epoll on Linux, kqueue on BSD and
macOS) watch every connection instead of one goroutine each, so idle connections cost almost nothing and buffers
are read and written without copying.

anet is derived from [CloudWeGo netpoll](https://github.com/cloudwego/netpoll) v0.7.5 (Apache-2.0). See
[NOTICE](NOTICE) for what changed.

```sh
go get github.com/alolainc/anet
```

Requires Go 1.27.

## Server and client

```go
ln, _ := anet.CreateListener("tcp", ":8080")
el, _ := anet.NewEventLoop(func(ctx context.Context, c anet.Connection) error {
	msg, _ := c.Reader().ReadString(c.Reader().Len())
	c.Writer().WriteString(msg)
	return c.Writer().Flush()
})
el.Serve(ln)
```

```go
conn, _ := anet.DialConnection("tcp", "127.0.0.1:8080", time.Second)
conn.Writer().WriteString("hello")
conn.Writer().Flush()
reply, _ := conn.Reader().ReadString(5)
```

More in [examples](examples): echo, multiplexing, streaming, a TCP proxy and a broadcast server.

## Handlers run on the poller by default

`OnRequest` runs directly on the poller, which saves a goroutine hand-off per request. The handler must not block:
`Reader` methods return `ErrNotEnoughData` instead of waiting, and `Flush` does not wait for a slow peer.

If your handler waits for data, calls another service or takes a contended lock, create the loop with
`anet.WithGoroutineRequest()`. The default is also off when `WithOnConnect` is set. Details are in the
documentation of `WithInlineRequest`.

## UDP

`NewPacketLoop` serves a `*net.UDPConn` on the pollers and calls a handler for every datagram, with the sender as
a `netip.AddrPort` and no allocation per datagram. Any UDP socket works, including multicast ones from
`net.ListenMulticastUDP`.

```go
conn, _ := net.ListenUDP("udp", &net.UDPAddr{Port: 14550})
loop, _ := anet.NewPacketLoop(func(ctx context.Context, conn *net.UDPConn, data []byte, from netip.AddrPort) {
	conn.WriteToUDPAddrPort(data, from) // data is only valid during the call
})
loop.Serve(conn)
```

See [examples/udp](examples/udp) for a telemetry receiver with multicast.

## Accepting on every poller

`WithReusePort` makes every poller accept new connections instead of one, which helps servers that accept many
short connections. Create the listener with `CreateReusePortListener`; the same code works on every platform:

| Platform | How |
|---|---|
| Linux, FreeBSD | one listener per poller (`SO_REUSEPORT` / `SO_REUSEPORT_LB`); the kernel spreads connections evenly |
| macOS, NetBSD, OpenBSD, DragonFly | the pollers share one listener; whichever wakes first accepts (less even) |
| Windows | no effect: the portable backend accepts on one goroutine driven by the Go runtime |

## Logging

anet logs to stderr by default. `Config.LoggerOutput` redirects the text log; `Config.Logger` takes a
`*slog.Logger` and receives structured records (level WARN for problems, INFO for recoveries, attribute
`lib=anet`).

## Packages

| Package | Purpose |
|---|---|
| `anet` | Connections, event loop, dialer, listener, nocopy `Reader`/`Writer`, socket tuning |
| `anet/mux` | Multiplex many requests over one connection |
| `anet/tee` | Send one stream of frames to many connections, with a policy for slow receivers |

## Project layout

The layout follows the Go team's guide [Organizing a Go module](https://go.dev/doc/modules/layout) and is close to
[gnet v2](https://github.com/panjf2000/gnet): the public API at the root, implementation details under `internal/`,
optional features as subpackages.

```
anet/                    package anet: the public API
├── anet.go              entry points (Configure, NewEventLoop, DialConnection, CreateListener, ...)
├── backend*.go          platform backends behind the entry points
├── connection*.go       connections served by the pollers
├── eventloop.go server.go options.go config.go errors.go
├── net_*.go             dialer, listener and socket setup (partly derived from the Go standard library)
├── nocopy*.go           nocopy Reader, Writer and LinkBuffer
├── packet*.go           UDP: PacketLoop
├── portable_*.go        connections on top of package net (Windows)
├── internal/
│   ├── netpoll/         epoll and kqueue pollers, FDOperator, poller manager and load balancing
│   ├── socket/          socket system calls (golang.org/x/sys/unix): readv, sendmsg, recvfrom, socket options
│   ├── errs/            error values (re-exported by anet)
│   ├── logging/         shared logger
│   ├── runner/          runs callbacks on the goroutine pool
│   └── testutil/        test assertions
├── mux/                 request multiplexing
├── tee/                 one stream to many connections
├── examples/            runnable examples
├── benchmarks/          benchmark harness (separate module)
└── scripts/             lint and test helpers
```

`nocopy*.go` stays in the root package on purpose: `Reader`, `Writer` and `LinkBuffer` are the core of the
Connection API, and the connection code calls unexported `LinkBuffer` methods. Moving them out would either expose
those methods or hide the buffer documentation behind aliases.

### Design patterns

| Pattern | Where | Why |
|---|---|---|
| Reactor | `internal/netpoll`, `connection_reactor.go` | a few pollers wait on all sockets and dispatch ready ones |
| Bridge | `anet.go` → `backend` (`backend_reactor.go`, `backend_portable.go`) | one public API, documented once, with a platform implementation behind it |
| Functional options | `options.go` (`WithReadTimeout`, `WithInlineRequest`, ...) | optional settings without breaking the `NewEventLoop` signature |
| Strategy | `LoadBalance` (round robin, random), `tee.Policy`, kqueue wake-up (`waker_*.go`) | choose how connections, slow receivers or poller wake-ups are handled |
| Observer | `OnPrepare`, `OnConnect`, `OnRequest`, `OnDisconnect`, close callbacks | the application reacts to connection events |
| Object pool | operator cache, `LinkBuffer` nodes, barriers, goroutine pool | avoid allocations; idle connections return their buffers to the pool |
| Adapter | `stdConn` (portable), `NewReader`/`NewWriter` | expose `net.Conn` and `io.Reader`/`io.Writer` as anet types |

## Low latency on slow networks

TCP queues data in the kernel when the network is slow, and the peer gets it late. `WithNotSentLowat`,
`WithSendBuffer` and `SocketTuner` keep that queue short. The `tee` package then drops old frames (`DropOldest`,
`MaxAge`) instead of sending stale data. In a simulated 2 Mbit link they cut the age of received frames from about
1.7 s to 0.2 to 0.3 s; see [benchmarks/README.md](benchmarks/README.md) for the setup and the limits of that test.

## Performance

For plain echo traffic anet performs about like netpoll and Go's `net` package on one machine; it is not faster.
Its advantage is memory: a connection waiting for its next request gives its read and write buffers back to a
shared pool, so it holds about 2 to 3 KB. With 5000 connections that made a few requests and went idle, the
server's RSS is about 24 MB, like gnet, against 75 MB for Go's `net` and 71 MB for netpoll. Numbers, methods and
caveats are in [benchmarks/README.md](benchmarks/README.md). Measure your own workload before choosing.

## Platforms

Linux uses epoll. macOS and the BSDs use kqueue; macOS and FreeBSD wake the poller with a user event, while
OpenBSD, NetBSD and DragonFly, whose `syscall` package has no `EVFILT_USER`, use a self-pipe. Windows uses a
portable backend on top of `net` with the same API, UDP included.

Tests run on Linux. The other systems are built and vetted in CI (`cross-build`) but not run, so treat them as
less proven.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Licensed under the [Apache License 2.0](LICENSE).
