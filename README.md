# grpc-tap

Real-time gRPC traffic viewer — proxy daemon + TUI client.

grpc-tap sits between your application and your gRPC server, capturing every call and displaying it in an interactive
terminal UI. Inspect requests, view headers, copy bodies, and replay calls — all without changing your application code.

## Installation

### Go

```bash
go install github.com/mickamy/grpc-tap@latest
go install github.com/mickamy/grpc-tap/cmd/grpc-tapd@latest
```

### Build from source

```bash
git clone https://github.com/mickamy/grpc-tap.git
cd grpc-tap
make install
```

## Quick start

**1. Start the proxy daemon**

```bash
# Proxy listens on :8080, forwards to upstream gRPC server on :9000
grpc-tapd -listen=:8080 -upstream=http://localhost:9000
```

**2. Point your application at the proxy**

Connect your app to the proxy port (`:8080`) instead of the upstream port. No code changes needed — grpc-tapd speaks
native gRPC, gRPC-Web, and Connect protocols.

**3. Launch the TUI**

```bash
grpc-tap localhost:9090
```

All gRPC calls flowing through the proxy appear in real-time.

## Usage

### grpc-tapd

```
grpc-tapd — gRPC proxy daemon for grpc-tap

Usage:
  grpc-tapd [flags]

Flags:
  -listen    client listen address (required)
  -upstream  upstream gRPC server address (required)
  -grpc      gRPC server address for TUI (default: ":9090")
  -version   show version and exit
```

### grpc-tap

```
grpc-tap — Watch gRPC traffic in real-time

Usage:
  grpc-tap [flags] <addr>

Flags:
  -version  Show version and exit
```

`<addr>` is the gRPC address of grpc-tapd (e.g. `localhost:9090`).

## Keybindings

### List view

| Key               | Action                               |
|-------------------|--------------------------------------|
| `j` / `↓`         | Move down                            |
| `k` / `↑`         | Move up                              |
| `Ctrl+d` / `PgDn` | Half-page down                       |
| `Ctrl+u` / `PgUp` | Half-page up                         |
| `/`               | Incremental search                   |
| `s`               | Toggle sort (chronological/duration) |
| `Enter`           | Inspect call                         |
| `e`               | Toggle error filter                  |
| `a`               | Analytics view                       |
| `Esc`             | Clear search filter                  |
| `q`               | Quit                                 |

### Inspector view

| Key       | Action                       |
|-----------|------------------------------|
| `j` / `↓` | Scroll down                  |
| `k` / `↑` | Scroll up                    |
| `c`       | Copy request body            |
| `C`       | Copy response body           |
| `e`       | Edit request & resend        |
| `q`       | Back to list                 |

### Analytics view

| Key       | Action                                  |
|-----------|-----------------------------------------|
| `j` / `↓` | Move down                               |
| `k` / `↑` | Move up                                 |
| `Ctrl+d`  | Half-page down                          |
| `Ctrl+u`  | Half-page up                            |
| `s`       | Cycle sort (total/count/avg/error rate) |
| `q`       | Back to list                            |

## How it works

```
┌─────────────┐      ┌───────────────────────┐      ┌─────────────────┐
│ Application │─────▶│  grpc-tapd (proxy)    │─────▶│ gRPC Server     │
└─────────────┘      │                       │      └─────────────────┘
                     │  captures calls       │
                     │  via HTTP/2 proxy     │
                     └───────────┬───────────┘
                                 │ gRPC stream
                     ┌───────────▼───────────┐
                     │  grpc-tap (TUI)       │
                     └───────────────────────┘
```

grpc-tapd acts as an HTTP/2 reverse proxy that transparently forwards gRPC traffic to the upstream server. It captures
request/response headers, bodies, status codes, and timing for each call. Events are streamed to connected TUI clients
via gRPC.

### Supported protocols

- **gRPC** (HTTP/2, `application/grpc`)
- **gRPC-Web** (`application/grpc-web`)
- **Connect** (`application/connect+proto`, `application/connect+json`)

### Edit & Resend

Press `e` in the inspector to open the captured request body in `$EDITOR` as JSON (field numbers as keys). After
editing, the modified request is sent to the upstream server via the proxy, and the result appears in the event stream.

## License

[MIT](./LICENSE)
