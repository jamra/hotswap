# hotswap

Zero-downtime hot reloading for Go web servers using socket takeover.

When you change your code, hotswap rebuilds your server and transfers the listening socket to the new process using [SCM_RIGHTS](https://man7.org/linux/man-pages/man7/unix.7.html). The old process drains existing connections while the new process immediately starts accepting — no dropped requests.

## Installation

```bash
go install github.com/jamra/hotswap/cmd/gohmr@latest
```

## Quick Start

**1. Add takeover support to your server:**

```go
package main

import (
    "net/http"
    "time"
    "github.com/jamra/hotswap/takeover"
)

func main() {
    mux := http.NewServeMux()
    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("Hello, World!"))
    })

    // Use takeover.ListenAndServe instead of http.ListenAndServe
    takeover.ListenAndServe(":8080", mux, takeover.Options{
        DrainTimeout: 30 * time.Second,
    })
}
```

**2. Run with the CLI:**

```bash
gohmr -watch .
```

**3. Edit your code** — the server hot-swaps automatically.

## How It Works

### The Problem

Normally, restarting a Go server means:
1. Stop old process (close listening socket)
2. Start new process (bind to port)
3. Gap where connections are refused

### The Solution: Socket Takeover

hotswap uses Unix domain sockets and `SCM_RIGHTS` to pass the listening file descriptor from the old process to the new one:

```
┌─────────────────┐     file change      ┌─────────────────┐
│   gohmr CLI     │ ──────────────────►  │   go build      │
│   (watcher)     │                      └────────┬────────┘
└────────┬────────┘                               │
         │                                        ▼
         │ starts new process          ┌─────────────────┐
         │ with HMR_TAKEOVER=1         │  New Process    │
         └────────────────────────────►│                 │
                                       └────────┬────────┘
                                                │
         Unix socket (SCM_RIGHTS)               │
         ┌──────────────────────────────────────┘
         ▼
┌─────────────────┐
│  Old Process    │  ─── drains connections ──► exits
└─────────────────┘
```

### The Protocol

When a file changes, the CLI starts a new process with `HMR_TAKEOVER=1`. The new process connects to the old one via a Unix socket and they perform a handshake:

```
New Process                              Old Process
    │                                         │
    │──── Hello {version} ───────────────────►│
    │◄─── HelloAck {version} ─────────────────│
    │                                         │
    │──── RequestSockets ────────────────────►│
    │◄─── SocketsTransfer {count} ────────────│
    │◄─── [file descriptors via SCM_RIGHTS] ──│
    │                                         │
    │──── SocketsAck ────────────────────────►│
    │                                         │  (closes listener,
    │                                         │   new process binds)
    │──── StartDrain ────────────────────────►│
    │◄─── DrainStarted ───────────────────────│
    │                                         │
    │  [accepting new connections]            │  [draining, then exit]
```

**Key insight:** The old process only releases the socket after the new process has received it. There's never a moment when no process is accepting connections.

### SCM_RIGHTS: Passing File Descriptors

`SCM_RIGHTS` is a Unix feature that allows passing file descriptors between processes over a Unix domain socket. The kernel duplicates the FD into the receiving process's FD table.

```go
// Sending (old process)
rights := unix.UnixRights(listenerFD)
unix.Sendmsg(conn, data, rights, nil, 0)

// Receiving (new process)
unix.Recvmsg(conn, data, oob, 0)
fds := unix.ParseUnixRights(controlMessage)
listener := net.FileListener(os.NewFile(fds[0], "listener"))
```

### Graceful Drain

After the new process takes over:
1. Old process stops accepting new connections
2. Existing requests continue to completion
3. After `DrainTimeout` (default 30s), force close remaining connections
4. Old process exits

## CLI Options

```
gohmr [options]

Options:
  -watch    Directories to watch, comma-separated (default: ".")
  -o        Output binary path (default: "./tmp/main")
  -socket   Takeover socket path (default: "/tmp/gohmr-takeover.sock")
  -v        Verbose output
```

## Library Options

```go
takeover.Options{
    // Time to wait for existing connections to complete
    DrainTimeout: 30 * time.Second,

    // Unix socket for process communication
    SocketPath: "/tmp/gohmr-takeover.sock",

    // Called when a new process takes over
    OnTakeover: func() { log.Println("Being replaced!") },

    // Called when draining begins
    OnDrainStart: func() { log.Println("Draining...") },
}
```

## Environment Variables

| Variable | Description |
|----------|-------------|
| `HMR_TAKEOVER` | Set to `1` by CLI to trigger takeover mode |
| `HMR_SOCKET_PATH` | Override the default socket path |

## Architecture

```
hotswap/
├── cmd/gohmr/          # CLI tool
│   └── main.go         # Watch → Build → Start with takeover
├── takeover/           # Core library
│   ├── takeover.go     # Public API: ListenAndServe()
│   ├── protocol.go     # Wire protocol messages
│   ├── scm_rights.go   # SCM_RIGHTS FD passing
│   ├── server.go       # Old process: sends FDs
│   └── client.go       # New process: receives FDs
├── watcher/            # File watching
│   └── watcher.go      # fsnotify with debounce
├── builder/            # Build orchestration
│   └── builder.go      # go build wrapper
└── examples/
    └── basic/          # Example server
```

## Requirements

- **Unix-like OS** (Linux, macOS) — uses SCM_RIGHTS
- **Go 1.21+**

## Caveats

- **Unix only**: Windows doesn't support SCM_RIGHTS
- **Single address**: Currently supports one listener per server
- **TCP only**: Unix socket listeners not yet supported for takeover

## Inspiration

This technique is used in production by:
- [Cloudflare](https://blog.cloudflare.com/graceful-upgrades-in-go/)
- [Facebook's Proxygen](https://github.com/facebook/proxygen)
- [HAProxy](https://www.haproxy.com/blog/truly-seamless-reloads-with-haproxy-no-more-hacks/)

## License

MIT
