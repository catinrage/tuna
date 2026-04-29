# Tuna

Tuna is a high-throughput SOCKS5-over-NATS tunnel written in Go.

It keeps the MVP architecture intact:

- `tuna-entry` exposes a local SOCKS5 proxy.
- `tuna-exit` receives connect requests over NATS and opens the real outbound TCP connections.

The rewrite changes the implementation strategy to reduce per-connection overhead and keep the hot path simple.

## Architecture

```text
client/app
   |
   | SOCKS5
   v
+tuna-entry+
   |
   | NATS core pub/sub
   v
+ NATS server +
   |
   | NATS core pub/sub
   v
+tuna-exit+
   |
   | TCP
   v
remote target
```

## Performance-oriented changes

Compared with the Python proof of concept, this version is designed around throughput and latency first:

- Go runtime and `nats.go` instead of Python asyncio.
- One wildcard subscription per data direction instead of one subscription per tunnel.
- Per-session lock-free fast path using buffered channels.
- Configurable large chunk sizes for fewer publish calls.
- Tuned TCP sockets: `TCP_NODELAY`, keepalive, read buffer, write buffer.
- Bounded session queues so slow consumers fail fast instead of growing memory without bound.
- Request/reply is used only for connection setup. The data plane stays on plain pub/sub.

## Binaries

- `cmd/tuna-entry`
- `cmd/tuna-exit`

## Config files

Example configs are included as:

- `config.entry.example`
- `config.exit.example`

The files are JSON. Copy them to the filenames you want and pass them with `-config`.

## Build

```bash
/usr/local/go/bin/go build ./...
```

## Run

Start the exit worker first:

```bash
export NATS_PASSWORD='your-password'
/usr/local/go/bin/go run ./cmd/tuna-exit -config config.exit.example
```

Then start the entry process:

```bash
export NATS_PASSWORD='your-password'
/usr/local/go/bin/go run ./cmd/tuna-entry -config config.entry.example
```

Point an application at:

```text
socks5://127.0.0.1:1080
```

Quick test:

```bash
curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
```

The observed IP should be the exit server IP.

## Subject layout

With the default prefix `tuna`:

- Connect subject: `tuna.connect`
- Entry -> exit data: `tuna.up.<session-id>`
- Exit -> entry data: `tuna.down.<session-id>`

Frames are 1 byte of type plus payload:

- `D` + raw bytes
- `E` for EOF

## Operational notes

- The entry listener binds to `127.0.0.1` by default. Keep it that way unless you add authentication and firewalling.
- The NATS password is intentionally sourced from an environment variable by default.
- `chunk_size_bytes` must stay below the NATS server `max_payload` setting.
- If you push very large downloads, tune `chunk_size_bytes`, socket buffers, and NATS pending limits together.

## Repository layout

```text
cmd/
  tuna-entry/
  tuna-exit/
internal/
  app/
    entry/
    exit/
  config/
  session/
  tunnel/
```
