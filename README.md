# Tuna

Tuna is a high-throughput SOCKS5-over-NATS tunnel written in Go.

It keeps the MVP architecture intact:

- `tuna-entry` exposes a local SOCKS5 proxy.
- `tuna-exit` receives connect requests over NATS and opens the real outbound TCP connections.

The rewrite changes the implementation strategy to reduce broker overhead, lower connect-path interference, and keep the data path cheap under sustained downloads.

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
- Separate NATS connections for the control plane and the data plane so bulk traffic does not starve connect setup.
- Sharded fixed data subjects instead of per-session or wildcard-routed subjects.
- Compact binary framing with the session ID in the frame header.
- Pooled frame buffers to reduce allocation and GC pressure on busy links.
- Per-session bounded ring queue with backpressure timeout instead of raw channels.
- Adaptive read-side coalescing to merge bursty socket reads into fewer NATS publishes.
- Adaptive write-side batching to reduce syscall pressure on the receiving side.
- Tuned TCP sockets: `TCP_NODELAY`, keepalive, read buffer, write buffer.
- Idle-session cleanup to remove stale state after restarts or abandoned flows.
- Entry session IDs are unique across process restarts, which removes duplicate-session collisions after reconnects or deployments.
- Request/reply is used only for connection setup. The data plane stays on plain pub/sub.

## Binaries

- `cmd/tuna-entry`
- `cmd/tuna-exit`

## Config files

Example configs are included as:

- `config.entry.example`
- `config.exit.example`

The files are JSON. Copy them to the filenames you want and pass them with `-config`.

The NATS password lives in the config file instead of the environment.

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

With the default prefix `tuna` and `data_subject_shards = 16`:

- Connect subject: `tuna.connect`
- Entry -> exit data: `tuna.up.00` through `tuna.up.15`
- Exit -> entry data: `tuna.down.00` through `tuna.down.15`

Frames are binary:

- 1 byte frame type
- 8 byte session ID
- raw payload for data frames

A session is pinned to one shard by `sessionID % data_subject_shards`.

## Recommended tuning

The example configs assume you also tune NATS for larger payloads.

Recommended broker-side baseline:

```yaml
maxPayload: 2MB

metrics:
  enabled: true

resources:
  requests:
    cpu: "1000m"
    memory: "1Gi"
  limits:
    cpu: "4000m"
    memory: "4Gi"
```

If you keep `maxPayload: 2MB`, the example Tuna values are a reasonable starting point:

- `chunk_size_bytes: 1048576`
- `write_batch_bytes: 4194304`
- `data_subject_shards: 16`
- `read_coalesce_min_delay: 50us`
- `read_coalesce_max_delay: 500us`
- `write_coalesce_min_delay: 50us`
- `write_coalesce_max_delay: 500us`
- `request_timeout: 15s`

## Operational notes

- The entry listener binds to `127.0.0.1` by default. Keep it that way unless you add authentication and firewalling.
- `chunk_size_bytes` must stay below the NATS server `max_payload` setting. The `1048576` example value is meant for a `2MB` broker payload limit.
- Adaptive coalescing is a tradeoff: low delays preserve responsiveness, high delays reduce message count. The included defaults are biased toward throughput without adding millisecond-scale latency.
- `queue_backpressure_timeout` controls how long a session can fall behind before it is failed instead of buffering indefinitely.
- `session_idle_timeout` and `cleanup_interval` are there to reap stale sessions after entry/exit restarts or broken clients. Keep them enabled.
- `kubectl port-forward` is acceptable for quick testing and a bad idea for real tunnel traffic. Use a stable reachable NATS endpoint.

## Expected impact of the current codebase

Relative to the earlier Go version, the cumulative changes should move results in roughly these ranges when the broker is not CPU-throttled:

- Sharded data subjects: `1.2x` to `2x` better throughput at higher concurrency, with lower broker hot-spotting.
- Separate control/data NATS connections: `10%` to `30%` better connect latency under download load.
- Adaptive coalescing and batched writes: `10%` to `40%` better sustained download throughput, depending on traffic shape.
- Pooled frames and queue-based delivery: `5%` to `20%` lower CPU and better tail latency under load.
- Restart-safe session IDs and idle cleanup: mainly correctness and stability, but they remove duplicate-session failures and stale-session buildup that would otherwise look like random performance problems.

A reasonable expectation if your NATS server is sized correctly:

- download throughput: another `1.5x` to `2.5x` over the older single-subject Go build
- connect stability under browser-style parallelism: materially better
- latency under load: `10%` to `30%` better on mixed traffic

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
