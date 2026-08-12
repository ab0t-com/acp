# Running ACP with Docker

The official image bundles the **coordd** daemon (the ACP authority) plus the `acp` CLI and
`acp-mcp` bridge, as one small static image for `linux/amd64` and `linux/arm64`. This guide gets
you from `docker run` to a connected client.

> Image: `ab0tcom/acp` on Docker Hub. Two modes, same repo:
> - **Local (default)** — `:latest`, `:vX.Y.Z` → `coordd`, file-backed storage. What most run.
> - **Server DB mode (ACPDB)** — `:server`, `:vX.Y.Z-server` → `coordd-server`, an embedded database
>   for bounded/flat memory at scale. Same protocol, same flags.
>
> `:latest` / `:server` track the newest release; the pinned `:vX.Y.Z` tags match the binary release.

## 1. Start a daemon

**Local (default)** — file-backed storage:

```bash
docker run -d --name coordd \
  --restart on-failure:5 \
  -p 8443:8443 \
  -v acp-data:/data \
  ab0tcom/acp:latest
```

**Server DB mode (ACPDB)** — embedded database; identical flags, just the `:server` tag (it runs
`coordd-server` for you):

```bash
docker run -d --name acpdb \
  --restart on-failure:5 \
  -p 8443:8443 \
  -v acpdb-data:/data \
  ab0tcom/acp:server
```

Either is a full ACP authority — shared filesystem, mailbox, event log, CRDT, leases, presence. Three
things to know:

- **`-v acp-data:/data`** — coordd keeps *everything* here: your shared token, its self-signed TLS
  certificate, and all space state. Use a named volume (or a host path) so restarts and upgrades
  keep your data. Without it, a removed container loses everything.
- **`-p 8443:8443`** — the daemon's HTTPS port. Change the host side freely (`-p 9000:8443`).
- **`--restart on-failure:5`** — a *smart, bounded* restart: only on a crash (non-zero exit), at most
  5 times, then it stays down so a real fault surfaces instead of looping. **Do not use `always` or
  `unless-stopped`** — an unbounded restart loop masks crashes and can exhaust host resources (see
  [§ Restart policy](#restart-policy)).

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
docker run -d --name coordd --restart on-failure:5 -p 8443:8443 -v acp-data:/data \
  ab0tcom/acp:latest -addr :8443 -data /data -hosts coordd.example.com,10.0.0.5
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

Run `docker run --rm ab0tcom/acp:latest -h` for the full list. Group commit and the awareness HTTP
tier are on by default; the awareness **WebSocket** transport is opt-in via `-awareness-ws`.

## 5. docker-compose

```yaml
services:
  coordd:
    image: ab0tcom/acp:latest
    command: ["-addr", ":8443", "-data", "/data", "-hosts", "coordd.example.com", "-awareness-ws"]
    ports:
      - "8443:8443"
    volumes:
      - acp-data:/data
    # Smart, BOUNDED restart: on-failure only, capped at 5. NEVER always/unless-stopped.
    restart: on-failure
    deploy:
      restart_policy:
        condition: on-failure
        max_attempts: 5
        window: 120s
    healthcheck:
      test: ["CMD", "wget", "-qO-", "--no-check-certificate", "https://127.0.0.1:8443/v1/healthz"]
      interval: 30s
      timeout: 5s
      retries: 3
volumes:
  acp-data:
```

`docker compose up -d`, then read the token with `docker compose exec coordd cat /data/token`.

<a id="restart-policy"></a>
### Restart policy

Use a **smart, bounded** restart — `docker run --restart on-failure:5` (or the compose
`deploy.restart_policy` above, `condition: on-failure` + `max_attempts: 5`). It restarts coordd
*only* on a crash, at most 5 times, then leaves it stopped so a genuine fault is visible instead of
silently looping.

**Do not use `always` or `unless-stopped`.** An unbounded restart loop hides a crashing daemon,
churns the volume, and can starve the host — for that reason those policies are **banned on our own
infrastructure**, and we recommend the bounded policy everywhere. (Restart policy is a *runtime*
setting — it can't be baked into the image; set it on `docker run` / compose.)

## 6. Upgrading

Pull the new tag and recreate the container — the volume carries your state across:

```bash
docker pull ab0tcom/acp:latest
docker rm -f coordd
docker run -d --name coordd --restart on-failure:5 -p 8443:8443 -v acp-data:/data ab0tcom/acp:latest
```

Pin a version (`ab0tcom/acp:v0.2.0`) in production and bump deliberately. See `CHANGELOG.md`.

## 7. High availability (cluster)

For a multi-node Raft cluster, each node runs its own container with `-raft-addr`, `-node-id`, and
peer flags, and node-to-node traffic uses cluster-CA mTLS. The container model is the same (one
volume per node); the flags are covered by the **acp-cluster** skill and `OPERATIONS.md`.

## 8. Build it yourself (from the released binaries)

The image does **not** build from source — the `Dockerfile` in this repo installs the **released,
checksum-verified binaries** (the same tarballs `install.sh` fetches). That means you can read the
`Dockerfile` and rebuild the *exact* official image using only public artifacts:

```bash
docker build --build-arg VERSION=v0.2.0 -t acp:local .
```

It fetches `acp_<version>_linux_<arch>.tar.gz` from the release downloads and verifies its SHA-256
against `checksums.txt` before installing — if the checksum doesn't match, the build fails. Same
chain of trust as `install.sh`; no source, no compiler in the image.

Server DB mode (ACPDB) — build the server variant, which runs `coordd-server`:

```bash
docker build --build-arg VERSION=v0.2.0 --build-arg ARCHIVE=acp-server --build-arg DAEMON=coordd-server \
  --build-arg BINS="coordd-server acp-server acp-mcp-server" -t acpdb:local .
```

Maintainers publish the official multi-arch images with `sop-docker-release.sh` (verify → build → push).

## Troubleshooting

- **Client TLS error / SAN mismatch** → the cert doesn't list your connect address; restart with
  `-hosts <that host/IP>` (a new cert is generated).
- **`401 invalid token`** → wrong token; re-read `docker exec coordd cat /data/token`.
- **State gone after `docker rm`** → you didn't mount `-v ...:/data`; always use a volume.
- **Health shows fewer capabilities than expected** → some are opt-in flags (e.g. `-awareness-ws`).
