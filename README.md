# SyncForge

SyncForge is an alpha collaborative plain-text editing prototype built with a TypeScript sequence CRDT and a Go WebSocket synchronization service. It includes browser-local offline persistence, reconnect and replay, durable server operation history, optional document-level token access, and a two-tab demo.

> **Status:** The original six-phase alpha roadmap is complete. SyncForge is a working prototype, not a production-ready collaborative editing service. See [BUILD.md](./BUILD.md) for the architecture, implementation details, operating instructions, verification steps, and remaining work.

## Features

- Deterministic text insert/delete operations across replicas, including reordered and duplicate delivery.
- Browser IndexedDB snapshots and a pending-operation queue for offline edits.
- WebSocket room routing, protocol validation, bounded operation replay, and reconnect.
- Per-document append-only server logs with periodic checkpoints and restart recovery.
- Optional bearer-token authorization with explicit document allowlists.
- Throttled ephemeral cursor presence with peer-leave notifications.
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
- Per-document replay is capped at 10,000 operations or 32 MiB; there is no global disk, memory, connection, or request-rate quota.
- Tokens are shared bearer credentials loaded at startup; there are no user identities, token lifecycle APIs, or operation attribution.
- The TipTap binding and demo are plain text. Rich-text semantics, selections and participant lists, version history, safe tombstone collection, and production operational guarantees are not implemented.

For the component-level description, protocol behavior, data durability details, and a more complete list of follow-up work, read [BUILD.md](./BUILD.md).
