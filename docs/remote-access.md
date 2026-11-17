# Remote access + the orchestrator Console

Hyperplane is local-first and binds loopback with no auth. To use it **securely
from anywhere** (e.g. a mini-PC at home reached over a Cloudflare Tunnel) two
things are added, both off by default so the local posture is unchanged:

1. **A bearer-token gate** (`HYPERPLANE_TOKEN`) on the API, artifacts, SSE, and
   the terminal WebSocket.
2. **An orchestrator Console** in the web UI: PTY-backed **Orchestrator** and
   **Shell** tabs with live environment visibility.

## The auth model

- **Loopback stays open.** A direct `127.0.0.1` request with no proxy headers is
  trusted — the local CLI and `npm run dev` need no token.
- **Anything that looks remote needs the token.** A Cloudflare Tunnel dials the
  API from loopback, so we treat any request carrying proxy/forwarding headers
  (`Cf-Connecting-Ip`, `X-Forwarded-*`, …) as remote regardless of peer IP. Those
  must present the token.
- **Fails closed.** If `HYPERPLANE_TOKEN` is unset, remote requests get `403`
  (remote access disabled) — you can't accidentally expose an open instance.
- The token is accepted as `Authorization: Bearer …`, `?token=…`, or the
  `hp_token` cookie. The web login exchanges it for a `SameSite=Strict`,
  `HttpOnly`, `Secure` cookie so `fetch`, `<img>`, SSE, and the terminal socket
  all authenticate uniformly. The terminal WebSocket also checks `Origin`.

Generate a token:

```bash
openssl rand -hex 32
```

## Single-origin deployment (recommended for a tunnel)

The API can serve the built web bundle itself (`HYPERPLANE_WEB_DIR`), so the
whole app is one origin on one port — the tunnel points at exactly one service
and auth is uniform. The PTY also runs wherever the API runs, so run the API
**natively on the host** to give the Console real host visibility.

Install Go 1.26, Node 22+, Docker with Compose and authenticate Codex on the
backend host. `scripts/deploy-host.sh` runs `make setup` to build the native API
and web bundle, then starts `scripts/run-local.sh` in the foreground. PostgreSQL
runs in Docker. See [runtime setup](runtime.md#setup-and-configuration) for the
packaged login command and configuration.

```bash
cd ~/hyperplane
cp .env.deploy.example .env.deploy   # then edit: set HYPERPLANE_TOKEN
./scripts/deploy-host.sh             # builds and serves in the foreground
```

Ctrl-C stops this foreground API. For unattended operation, run the launcher
under your chosen service manager; [the example systemd unit](../infra/hyperplane-api.service)
uses the same launcher. Edit its service user, paths and Node location for your
host, and authenticate Codex as that user. Leave `HYPERPLANE_ORCHESTRATOR_CMD`
unset to use the SDK console with project and workflow tools. After updating the code, rebuild and restart
through the same deployment entrypoint. `make down` stops PostgreSQL while
retaining its volume.

`.env.deploy` keys:

| key | meaning |
|---|---|
| `HYPERPLANE_TOKEN` | **required** — the bearer secret |
| `HYPERPLANE_ADDR` | bind addr (default `127.0.0.1:4252`) |
| `HYPERPLANE_ALLOWED_ORIGINS` | your tunnel origin, e.g. `https://hyperplane.example.org` |
| `HYPERPLANE_ORCHESTRATOR_CMD` | trusted host-program override for the Orchestrator tab (default: the SDK console with project/workflow tools) |

## Cloudflare Tunnel

Expose the single loopback origin without opening any inbound port.

```bash
# one-time on the host
curl -L https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 \
  -o /usr/local/bin/cloudflared && chmod +x /usr/local/bin/cloudflared
cloudflared tunnel login
cloudflared tunnel create hyperplane
```

`~/.cloudflared/config.yml`:

```yaml
tunnel: <TUNNEL_ID>
credentials-file: /home/example/.cloudflared/<TUNNEL_ID>.json
ingress:
  - hostname: hyperplane.example.org
    service: http://127.0.0.1:4252
  - service: http_status:404
```

```bash
cloudflared tunnel route dns hyperplane hyperplane.example.org
cloudflared tunnel run hyperplane           # or install as a service:
sudo cloudflared service install
```

Then set `HYPERPLANE_ALLOWED_ORIGINS=https://hyperplane.example.org` in
`.env.deploy`, re-run the deploy script, and open the hostname — the login screen
asks for the token once, then the Console is live.

> Defense in depth: putting **Cloudflare Access** (SSO) in front of the hostname
> is recommended on top of the token, since the Console's Shell tab is a real
> shell on the host.

## The Console

The Orchestrator tab runs the packaged SDK console on the backend host.
It retains a thread while connected and supports `/new`, `/exit` and Ctrl-C.
Ask it to create a project or save a workflow: its application tools persist those
records through the same backend validation as the editor. Review and run the
saved workflow from Workflows. Filesystem writes, network access and ambient host
MCP are disabled; the only application integration is the project/workflow
management connection. Codex may expose isolated SDK helpers alongside it.
Reconnecting starts a new conversation; this terminal is not a persisted workflow
delivery or approval recovery mechanism.

The Shell tab runs a trusted host shell. Environment status displays database,
SDK and local-provider readiness. Both terminal lifetimes belong to application
shutdown; explicit host-program overrides retain their own permissions.
See [console runtime rules](runtime.md#interactive-console).
