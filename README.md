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
- One fixed subject per direction instead of per-session subjects, with the session ID carried in a compact binary frame header.
- Per-session lock-free fast path using buffered channels.
- Larger default frame size to cut NATS publish count roughly in half versus the previous `256 KiB` setting.
- Short read-side coalescing window to merge bursty TCP reads into fewer NATS publishes.
- Short write-side coalescing window plus batched socket writes to reduce syscall pressure on downloads.
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

The NATS password now lives in the config file instead of the environment.

## Build

```bash
/usr/local/go/bin/go build ./...
```

## Test

```bash
/usr/local/go/bin/go test ./...
/usr/local/go/bin/go test -race ./...
```

## Run

Start the exit worker first:

```bash
/usr/local/go/bin/go run ./cmd/tuna-exit -config config.exit.example
```

Then start the entry process:

```bash
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
- Entry -> exit data: `tuna.up`
- Exit -> entry data: `tuna.down`

Frames are binary:

- 1 byte frame type
- 8 byte session ID
- raw payload for data frames

## Operational notes

- The entry listener binds to `127.0.0.1` by default. Keep it that way unless you add authentication and firewalling.
- `chunk_size_bytes` must stay below the NATS server `max_payload` setting. If `maxPayload` is left unset in NATS, the effective server default is typically `1 MiB`, so the default `524288` byte frame size here is intentionally conservative.
- `read_coalesce_delay` is the max extra wait after a socket read before publishing a frame. Raising it reduces publish count and improves throughput, but increases latency for very small interactive packets.
- `write_coalesce_delay` and `write_batch_bytes` only affect NATS-to-socket writes. They do not change NATS payload size; they reduce write syscalls on the receiving side.
- If you push very large downloads, tune `chunk_size_bytes`, coalescing delays, socket buffers, and NATS pending limits together.
- For your current single-node single-replica manifest, the next NATS-side knobs to make explicit are `maxPayload: 1MB`, enabled metrics, and fixed CPU and memory requests/limits so the broker does not get throttled during bulk transfers.

## Expected impact

These changes stay inside the existing all-data-over-NATS design. They do not remove the broker hop, so they are not a replacement for a different transport architecture.

What they should improve in practice:

- `chunk_size_bytes` raised from `256 KiB` to `512 KiB`: about `1.3x` to `1.8x` better bulk throughput when the flow is publish-count bound.
- `read_coalesce_delay` at `250us`: often `15%` to `35%` fewer NATS messages for bursty traffic, with a small latency tradeoff on tiny packets.
- `write_coalesce_delay` plus `write_batch_bytes`: usually `10%` to `25%` better download throughput and lower CPU/syscall overhead on the receiver.
- Larger default socket buffers and deeper queues: mostly a stability improvement under high-bandwidth, high-RTT flows, with `5%` to `15%` throughput gain when the old buffers were too small.
- Fixed `tuna.up` / `tuna.down` subjects with binary session headers: usually another `5%` to `15%` less broker and client CPU at high message rates, with a modest latency improvement from removing wildcard subject parsing.

Overall expectation if the broker is not CPU-throttled:

- latency: usually unchanged to `10%` better for downloads, slightly worse for isolated tiny packets because of the `250us` batching window
- throughput: commonly `1.5x` to `2.8x` better than the previous Go version on sustained transfers

## Automation

- `.github/workflows/test.yml` runs formatting checks, unit tests, and a full build on pushes to `main` and pull requests.
- `.github/workflows/release.yml` builds release archives for Linux, macOS, and Windows on every push to `main` and publishes them as GitHub prereleases. Real `v*` tags are still published as normal releases.

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
