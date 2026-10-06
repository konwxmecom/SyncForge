# Reverse proxy and TLS deployment

This project is designed for local development first. For a real deployment, use a trusted reverse proxy in front of the demo and a private backend network for the Go service.

## Recommended layout

- public internet: HTTPS reverse proxy
- private internal port: Go sync server on `127.0.0.1:8080` or a private Docker network
- front-end: Vite demo on `127.0.0.1:5173`
- TLS termination: nginx, Caddy, or a managed load balancer

## Nginx example

A sample nginx configuration is in [../deploy/nginx.conf.example](../deploy/nginx.conf.example).

The important parts are:

- redirect port 80 to HTTPS
- proxy `/` to the browser demo
- proxy `/sync` to the sync server with WebSocket upgrade headers
- keep the raw Go service off public internet
- do not route `/metrics` publicly; expose it only to a trusted monitoring network
- ensure `AUTH_FILE` and `DATA_DIR` are protected on the host

## Important security notes

- do not expose the raw Go service on `:8080` to the internet
- use `wss://` only behind a TLS terminator
- keep the auth file outside the web root and with restrictive permissions
- rotate bearer tokens and review document grants regularly
- test origin and host validation before enabling external access

## Minimal production checklist

1. Put the Go service on a private network or localhost-only binding.
2. Terminate TLS at the proxy.
3. Add a health endpoint check before routing traffic.
4. Keep logs and durable data in a protected location.
5. Run the app with explicit `AUTH_FILE` and `DATA_DIR` values.
6. Validate the `/sync` route with WebSocket handshakes before launch.
7. Configure off-host backups and periodically test restores into a separate data directory.

This does not replace a full security review or a hardened deployment process, but it is the next practical step beyond local development.
