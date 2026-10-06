# SyncForge

SyncForge is an alpha collaborative plain-text editing prototype built with a TypeScript sequence CRDT and a Go WebSocket synchronization service. It includes browser-local offline persistence, reconnect and replay, durable server operation history, required document-level token access, and a two-tab demo.

> **Status:** The plain-text collaborative alpha scope is complete. SyncForge is a working prototype, not a production-ready collaborative editing service. See [BUILD.md](./BUILD.md) for architecture, operating instructions, verification, and future-scope boundaries.

## Features

- Deterministic text insert/delete operations across replicas, including reordered and duplicate delivery.
- Browser IndexedDB snapshots and a pending-operation queue for offline edits.
- WebSocket room routing, protocol validation, bounded operation replay, and reconnect.
- Per-document append-only server logs with periodic checkpoints and restart recovery.
- Required bearer-token authorization with explicit document allowlists.
- Throttled ephemeral cursor presence with peer-leave notifications.
- Loopback-first service defaults, connection/message limits, basic metrics, and private data backup tooling.
- A TipTap plain-text binding, browser demo, and reproducible CRDT workload benchmark.

## Requirements

- Node.js and npm compatible with the versions declared by the project dependencies.
- Go 1.22 or later.
- A modern browser with WebSocket and IndexedDB support.

## Quick start

Install dependencies and run the checks:

```sh
npm install
npm test
```

Create a local authorization file and restrict its permissions:

```sh
cp auth.example.json auth.local.json
chmod 600 auth.local.json
```

Replace the example token in `auth.local.json` with a randomly generated secret of at least 32 characters. This file is ignored by Git.

In one terminal, start the Go service:

```sh
AUTH_FILE=./auth.local.json npm run dev:server
```

In another terminal, start the browser demo:

```sh
npm run dev:demo
```

Open `http://localhost:5173/?doc=demo-room` in two browser tabs. Enter the configured token in both tabs and select **Connect**. The server refuses to start unless a valid `AUTH_FILE` is configured, and its default listener is loopback-only.

Build the static demo with:

```sh
npm run build:demo
```

## Docker local stack

A containerized local stack is available for a quick smoke test without the local TypeScript and Go tooling installed:

```sh
cp .env.example .env
cp auth.example.json auth.local.json
chmod 600 auth.local.json
# Replace the example token in auth.local.json with a random secret.
docker compose up --build
```

Then open `http://localhost:5173/?doc=demo-room` in a browser and enter the configured token. The demo uses Vite's proxy to route `/sync` traffic to the Go service on port `8080`. Both published ports bind to loopback only; for a custom host, set `VITE_SYNC_PROXY_URL` before starting the stack.

The local stack includes health checks for both services and waits for the sync server to become healthy before starting the demo. For a real deployment, place a TLS-terminating reverse proxy in front of the demo and keep the Go service on an internal network or private port.

## Back up and restore data

Stop the sync server before making a backup (press Ctrl-C for local development, or run `docker compose stop syncforge-server`). Then create a private archive outside the data directory:

```sh
scripts/backup-data.sh ./data ./backups/syncforge-$(date -u +%Y%m%dT%H%M%SZ).tar.gz
```

The script writes the archive atomically with owner-only permissions and refuses destinations inside `DATA_DIR` or symbolic links. Store a copy off the machine. To restore a trusted archive, keep the server stopped, extract it into a new owner-only directory, inspect the contents, and point `DATA_DIR` at that directory:

```sh
mkdir -m 700 ./restore-data
tar -tzf ./backups/syncforge-backup.tar.gz
tar -xzf ./backups/syncforge-backup.tar.gz -C ./restore-data
DATA_DIR=./restore-data AUTH_FILE=./auth.local.json npm run dev:server
```

Never extract an untrusted archive into a service data directory. Keep the previous data directory until the restored service has been verified.

## Reverse proxy and TLS guidance

A simple nginx example is included in [deploy/nginx.conf.example](deploy/nginx.conf.example). The pattern is:

- terminate TLS at the proxy
- proxy `/` to the Vite demo on `127.0.0.1:5173`
- proxy `/sync` to the Go service on `127.0.0.1:8080` using `proxy_pass http://127.0.0.1:8080;` with `proxy_http_version 1.1` and WebSocket upgrade support
- restrict network access to the service's internal port
- use a trusted certificate and avoid exposing the raw `:8080` port externally

For detailed deployment notes, see [docs/reverse-proxy.md](docs/reverse-proxy.md).

## Server configuration

The server listens on `127.0.0.1:8080` by default. Configure it with environment variables:

| Variable | Default | Description |
|---|---|---|
| `ADDR` | `127.0.0.1:8080` | HTTP and WebSocket listen address; use a private interface behind a TLS-terminating proxy for deployments |
| `DATA_DIR` | `./data` | Owner-only directory for durable logs and checkpoints |
| `AUTH_FILE` | required | Owner-only JSON file containing token-to-document grants; the server refuses to start without it |
| `MAX_CONNECTIONS` | `256` | Maximum simultaneous WebSocket connections per process |
| `MAX_CONNECTIONS_PER_DOCUMENT` | `32` | Maximum simultaneous connections to one document |
| `MAX_MESSAGES_PER_MINUTE` | `600` | Maximum inbound messages per connection per minute |

`GET /healthz` is a liveness response, `GET /readyz` indicates that startup configuration was accepted, and `GET /metrics` exposes low-cardinality Prometheus gauges for active connections and rooms. These endpoints have no authentication; keep the service on loopback/private networking and do not publish `/metrics` through a public reverse proxy.

A principal in the required authorization file has this shape:

```json
{
  "principals": [
    {
      "token": "<random-secret-at-least-32-characters>",
      "documents": ["demo-room"]
    }
  ]
}
```

Set restrictive permissions on both the authorization file and data directory. The demo sends the token in the WebSocket join message, not in the shareable URL. For remote deployments, configure TLS at a trusted reverse proxy and use `wss://`.

## Verification and benchmark

```sh
npm test
npm run build:demo
cd server && go test -race ./...
cd ..
npm run benchmark:core
```

The benchmark reports its runtime environment and workload, then checks that two CRDT replicas converge. Its measurements are local, in-process results—not WebSocket, disk, or production-capacity measurements.

## Known limitations

- This is a single-process alpha service; there is no distributed coordination or multi-instance consistency.
- Server logs are append-only, have no compaction, and need an external backup/restore process.
- Per-document replay is capped at 10,000 operations or 32 MiB. Configurable connection/message ceilings are initial safety limits, not capacity guarantees; there is no global disk or memory quota.
- Tokens are shared bearer credentials loaded at startup; there are no user identities, token lifecycle APIs, or operation attribution.
- The TipTap binding and demo are plain text. Rich-text semantics, selections and participant lists, version history, safe tombstone collection, and production operational guarantees are not implemented.

SyncForge's plain-text collaborative alpha implementation is complete, but the service is not production-ready. Rich text, identity management, distributed storage, automated operations, and other platform features remain future scope. For component details and boundaries, read [BUILD.md](./BUILD.md).
