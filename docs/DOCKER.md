# Running ACP with Docker

The official image bundles the **coordd** daemon (the ACP authority) plus the `acp` CLI and
`acp-mcp` bridge, as one small static image for `linux/amd64` and `linux/arm64`. This guide gets
you from `docker run` to a connected client.

> Image: `ab0t/acp` on Docker Hub. `:latest` tracks the newest release; `:vX.Y.Z` pins a version
> (matching the binary release of the same version).

## 1. Start a daemon

```bash
docker run -d --name coordd \
  -p 8443:8443 \
  -v acp-data:/data \
  ab0t/acp:latest
```

That's a full ACP authority — shared filesystem, mailbox, event log, CRDT, leases, presence. Two
things to know:

- **`-v acp-data:/data`** — coordd keeps *everything* here: your shared token, its self-signed TLS
  certificate, and all space state. Use a named volume (or a host path) so restarts and upgrades
  keep your data. Without it, a removed container loses everything.
- **`-p 8443:8443`** — the daemon's HTTPS port. Change the host side freely (`-p 9000:8443`).

Check it's healthy:

```bash
docker logs coordd
curl -k https://localhost:8443/v1/healthz     # {"status":"ok","protocol":"acp/1",...}
```

## 2. Connect a client

On first run coordd generates a **shared token** and a **pinned certificate** in `/data`. Pull them
out to point a client at the daemon:

```bash
docker exec coordd cat /data/token            # your bearer token
docker cp coordd:/data/cert.pem ./coordd-cert.pem
```

Then, with the `acp` CLI (installed locally, or run inside the container):

```bash
acp config init
acp config set docker server=https://localhost:8443 token=<token> cert=./coordd-cert.pem agent=me
acp config use docker
acp who
```

You can also run the CLI *inside* the image:

```bash
docker exec -e ACP_TOKEN=$(docker exec coordd cat /data/token) coordd acp who
```

## 3. TLS and hostnames (important)

coordd serves HTTPS with a **self-signed certificate** and clients pin it (they trust that exact
cert — see `THREAT_MODEL.md`). The certificate must list the hostname or IP clients connect to as a
SAN, or verification fails. When you expose the daemon beyond `localhost`, tell it which names to
certify with **`-hosts`**:

```bash
docker run -d --name coordd -p 8443:8443 -v acp-data:/data \
  ab0t/acp:latest -addr :8443 -data /data -hosts coordd.example.com,10.0.0.5
```

(Anything after the image name replaces the default command — always keep `-addr` and `-data`.)

## 4. Configuration

Pass flags after the image name. The most common:

| Flag | Purpose |
|---|---|
| `-addr :8443` | listen address/port |
| `-data /data` | state directory (keep this pointed at the volume) |
| `-hosts a,b` | hostnames/IPs to include as cert SANs |
| `-token <t>` | use a fixed shared token instead of a generated one |
| `-group-commit` | coalesce concurrent appends onto one fsync (default **on**; `-group-commit=false` to revert) |
| `-awareness-ws` | enable the WebSocket transport for live presence (ext-9) |
| `-max-docs N` | cap CRDT documents per space |

Run `docker run --rm ab0t/acp:latest -h` for the full list. Group commit and the awareness HTTP
tier are on by default; the awareness **WebSocket** transport is opt-in via `-awareness-ws`.

## 5. docker-compose

```yaml
services:
  coordd:
    image: ab0t/acp:latest
    command: ["-addr", ":8443", "-data", "/data", "-hosts", "coordd.example.com", "-awareness-ws"]
    ports:
      - "8443:8443"
    volumes:
      - acp-data:/data
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-qO-", "--no-check-certificate", "https://127.0.0.1:8443/v1/healthz"]
      interval: 30s
      timeout: 5s
      retries: 3
volumes:
  acp-data:
```

`docker compose up -d`, then read the token with `docker compose exec coordd cat /data/token`.

## 6. Upgrading

Pull the new tag and recreate the container — the volume carries your state across:

```bash
docker pull ab0t/acp:latest
docker rm -f coordd
docker run -d --name coordd -p 8443:8443 -v acp-data:/data ab0t/acp:latest   # same flags as before
```

Pin a version (`ab0t/acp:v0.1.7`) in production and bump deliberately. See `CHANGELOG.md`.

## 7. High availability (cluster)

For a multi-node Raft cluster, each node runs its own container with `-raft-addr`, `-node-id`, and
peer flags, and node-to-node traffic uses cluster-CA mTLS. The container model is the same (one
volume per node); the flags are covered by the **acp-cluster** skill and `OPERATIONS.md`.

## 8. Build it yourself

The image is built from `acp/Dockerfile` (multi-stage, `CGO_ENABLED=0`, static). To build locally
from a checkout of the source:

```bash
docker build --build-arg VERSION=dev -t acp:dev -f acp/Dockerfile acp/
```

Maintainers publish official multi-arch images with `sop-docker-release.sh` (build → smoke → push).

## Troubleshooting

- **Client TLS error / SAN mismatch** → the cert doesn't list your connect address; restart with
  `-hosts <that host/IP>` (a new cert is generated).
- **`401 invalid token`** → wrong token; re-read `docker exec coordd cat /data/token`.
- **State gone after `docker rm`** → you didn't mount `-v ...:/data`; always use a volume.
- **Health shows fewer capabilities than expected** → some are opt-in flags (e.g. `-awareness-ws`).
