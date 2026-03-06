# hotswap

Zero-downtime hot reloading for Go web servers using socket takeover.

When you change your code, hotswap rebuilds your server and transfers the listening socket to the new process using [SCM_RIGHTS](https://man7.org/linux/man-pages/man7/unix.7.html). The old process drains existing connections while the new process immediately starts accepting — no dropped requests.

## How it works

```
┌─────────────────┐     file change      ┌─────────────────┐
│   hotswap CLI   │ ──────────────────►  │   go build      │
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

## Installation

```bash
go install github.com/jamra/hotswap/cmd/gohmr@latest
```

## Usage

### CLI

Run your Go server with hot reloading:

```bash
gohmr -watch .
```

Options:

```
-watch    Directories to watch, comma-separated (default: ".")
-o        Output binary path (default: "./tmp/main")
-socket   Takeover socket path (default: "/tmp/gohmr-takeover.sock")
-v        Verbose output
```

### Library

Add takeover support to your server:

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

Then run with the CLI:

```bash
gohmr -watch .
```

## Example

```bash
# Terminal 1: Start the server
cd examples/basic
gohmr -watch .

# Terminal 2: Make requests
curl http://localhost:8080

# Terminal 3: Edit examples/basic/main.go
# Watch Terminal 1 — server rebuilds and restarts without dropping connections
```

## Options

### takeover.Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `DrainTimeout` | `time.Duration` | 30s | Time to wait for existing connections to complete |
| `SocketPath` | `string` | `/tmp/gohmr-takeover.sock` | Unix socket for process communication |
| `OnTakeover` | `func()` | nil | Called when a new process takes over |
| `OnDrainStart` | `func()` | nil | Called when draining begins |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `HMR_TAKEOVER` | Set to `1` by CLI to trigger takeover mode |
| `HMR_SOCKET_PATH` | Override the default socket path |

## Requirements

- Unix-like OS (Linux, macOS) — uses SCM_RIGHTS for file descriptor passing
- Go 1.21+

## License

MIT
