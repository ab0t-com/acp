# Running ACP with Docker

The official image bundles the **coordd** server (the ACP authority) plus the `acp` CLI and the
`acp-mcp` bridge, as one small static image for `linux/amd64` and `linux/arm64`. This guide gets
you from `docker run` to a connected agent.

> **New to running ACP?** [RUN-ACP.md](RUN-ACP.md) helps you choose between Docker and the plain program, and
> walks a laptop, a team and a company through the shortest path. This page is the Docker detail behind it.

> Image: `ab0tcom/acp` on Docker Hub. Two storage options, same repo:
> - **File storage (the default)**: `:latest`, `:vX.Y.Z` run `coordd`, which keeps plain files. What most people run.
> - **Database storage** (also called server DB mode, or ACPDB): `:server`, `:vX.Y.Z-server` run `coordd-server`,
>   an embedded database that keeps memory flat as a long history grows. Same protocol, same flags.
>
> `:latest` / `:server` track the newest release; the pinned `:vX.Y.Z` tags match the binary release.
> See [STORAGE-MODES.md](STORAGE-MODES.md) for which to pick.

## 1. Start a server

**File storage (default):**

```bash
docker run -d --name coordd \
  --restart on-failure:5 \
  -p 8443:8443 \
  -v acp-data:/data \
  ab0tcom/acp:v0.2.5
```

**Database storage:** identical flags, just the `-server` tag (it runs `coordd-server` for you):

```bash
docker run -d --name acpdb \
  --restart on-failure:5 \
  -p 8443:8443 \
  -v acpdb-data:/data \
  ab0tcom/acp:v0.2.5-server
```

Either is a full ACP server: shared filesystem, mailbox, event log, live documents, leases, presence. Three
things to know:

- **`-v acp-data:/data`**: coordd keeps *everything* here: your admin login's token, its self-signed TLS
  certificate, and all workspace state. Use a named volume so restarts and upgrades keep your data. Without it, a
  removed container loses everything. (The image runs as user 10001, so a named volume is simpler than a host
  folder.)
- **`-p 8443:8443`**: the server's HTTPS port. Change the host side freely (`-p 9000:8443`).
- **`--restart on-failure:5`**: a *bounded* restart: only on a crash (non-zero exit), at most 5 times, then it
  stays down so a real fault surfaces instead of looping. **Do not use `always` or `unless-stopped`** (see
  [Restart policy](#restart-policy)).

Check it's healthy:

```bash
docker logs coordd
curl -k https://localhost:8443/v1/healthz     # {"status":"ok","protocol":"acp/1",...}  (-k: a local probe only)
```

## 2. Connect yourself, then invite agents

A new volume starts with **one login per agent**. On its first start the server creates an admin login named
`owner` and writes its token to `/data/owner.token` (private, never printed). The log ends with the exact
`acp config set owner …` line that connects this machine, including the certificate fingerprint (`pin=sha256:…`).

Copy the token out of the volume into a private file, and read the fingerprint from the log:

```bash
mkdir -p ~/.acp/tokens
(umask 077; docker exec coordd cat /data/owner.token > ~/.acp/tokens/owner.token)
docker logs coordd 2>&1 | grep -o 'pin=sha256:[0-9a-f]*' | head -1
```

(With Docker Compose, section 5: `docker compose exec -T acp cat /data/owner.token`.)

Then connect with the `acp` CLI on your machine. This is the printed connect line, with `token_file` pointing at
your copy (the printed path is the one inside the container):

```bash
acp config set owner server=https://127.0.0.1:8443 token_file=~/.acp/tokens/owner.token \
   agent=owner pin=sha256:<fingerprint>
acp status                       # login: ok, owner as admin
acp invite --name codexB         # paste what it prints into the other agent
```

The other agent runs the one `acp join acpj1_…` line from that text and is connected with its own login: nothing
to copy by hand. An invite made through `127.0.0.1` only works for agents on this machine. For agents elsewhere,
connect your admin profile by the name they will use (and make sure the certificate covers it, section 3), so the
invites you create point there.

`acp invites` lists invites and who joined; `acp invites remove-member <id>` removes an agent an invite let in.

## 3. TLS and hostnames (important)

coordd serves HTTPS with a **self-signed certificate**, and clients pin it (an invite carries the fingerprint, so
joining agents trust the right server automatically; see [THREAT_MODEL.md](THREAT_MODEL.md)). The certificate must
list the hostname or IP clients connect to, or verification fails.

Inside a container the automatic certificate covers `localhost`, `127.0.0.1` and the container's own addresses,
not the Docker host's name or IP. So when agents on other machines will connect, tell it which names to certify
with **`-hosts`**:

```bash
docker run -d --name coordd --restart on-failure:5 -p 8443:8443 -v acp-data:/data \
  ab0tcom/acp:v0.2.5 -addr :8443 -data /data -hosts coordd.example.com,10.0.0.5,localhost,127.0.0.1
```

- **From v0.2.5 `-hosts` adds to the automatic names** (`-hosts-exact` keeps only the listed ones). On v0.2.4 and
  older it replaces them, so there list **every** name and address agents will use, plus `localhost,127.0.0.1`.
- Anything after the image name replaces the default command, so always keep `-addr` and `-data`.
- The certificate is made once, on the volume's first start, and **never rewritten** (agents have pinned it).
  Setting `-hosts` on an existing volume changes nothing. To re-make it: stop the container, move `cert.pem` and
  `key.pem` out of the volume, and start again with the right `-hosts`. Every agent that already joined must then
  re-join or update its `pin`.

For a public name with a normal certificate, put your own HTTPS proxy in front: see "Behind a proxy" in
[RUN-ACP.md](RUN-ACP.md).

## 4. Configuration

Pass flags after the image name. The most common:

| Flag | Purpose |
|---|---|
| `-addr :8443` | listen address/port |
| `-data /data` | state directory (keep this pointed at the volume) |
| `-hosts a,b` | extra names/IPs agents use, for the certificate (added to the automatic ones from v0.2.5; `-hosts-exact` = only these) |
| `-owner-name <name>` | name of the admin login a new volume makes (default `owner`) |
| `-shared-token` | start a **new** volume the old way: one shared key for everyone in `/data/token` (no invites) |
| `-data-key-file <file>` | encrypt shared file contents at rest (see [OPERATIONS.md](OPERATIONS.md)) |
| `-group-commit` | coalesce concurrent appends onto one fsync (default **on**; `-group-commit=false` to revert) |
| `-awareness-ws` | enable the WebSocket transport for live presence |
| `-max-docs N` | cap live documents per workspace |

Run `docker run --rm ab0tcom/acp:v0.2.5 -h` for the full list, and [OPERATIONS.md](OPERATIONS.md) for what each
does. Group commit and the presence HTTP tier are on by default; the presence **WebSocket** transport is opt-in
via `-awareness-ws`.

An existing volume keeps the login mode it has: one with `/data/token` stays on the shared key, one with logins
keeps them.

## 5. docker-compose

```yaml
services:
  acp:
    image: ab0tcom/acp:v0.2.5               # a fixed version; you choose when to upgrade
    command: ["-addr", ":8443", "-data", "/data",
              "-hosts", "coordd.example.com,localhost,127.0.0.1"]   # every name agents use
    ports:
      - "8443:8443"
    volumes:
      - acp-data:/data
    # Bounded restart: on-failure only, capped at 5. NEVER always/unless-stopped.
    restart: on-failure:5
    logging: { options: { max-size: "10m", max-file: "3" } }
    healthcheck:
      test: ["CMD", "wget", "-qO-", "--no-check-certificate", "https://127.0.0.1:8443/v1/healthz"]
      interval: 30s
      timeout: 5s
      retries: 3
volumes:
  acp-data:
```

Leave the `-hosts` line out if every agent runs on the Docker host itself. Then:

```bash
docker compose up -d
mkdir -p ~/.acp/tokens
(umask 077; docker compose exec -T acp cat /data/owner.token > ~/.acp/tokens/owner.token)
docker compose logs acp | grep -o 'pin=sha256:[0-9a-f]*' | head -1      # the certificate fingerprint
acp config set owner server=https://coordd.example.com:8443 token_file=~/.acp/tokens/owner.token \
   agent=owner pin=sha256:<fingerprint>
acp status
acp invite --name codexB
```

<a id="restart-policy"></a>
### Restart policy

Use a **bounded** restart: `docker run --restart on-failure:5`, or `restart: on-failure:5` in Compose. It
restarts coordd *only* on a crash, at most 5 times, then leaves it stopped so a genuine fault is visible instead of
silently looping.

**Do not use `always` or `unless-stopped`.** An unbounded restart loop hides a crashing server, churns the volume,
and can starve the host. We ban those policies on our own infrastructure and recommend the bounded policy
everywhere. (Restart policy is a *runtime* setting; it can't be baked into the image.)

A bounded restart covers crashes, not a host reboot. To start on boot, run `docker compose up -d` from a small
systemd unit. Exit code `78` means "a problem only a person can fix" (a bad flag, a damaged data folder, a wrong
encryption key): restarting won't help, so read the log line that starts `OPERATOR ACTION REQUIRED`.

## 6. Upgrading

Change the tag, pull, and recreate. The volume carries your state across:

```bash
docker compose pull && docker compose up -d      # after changing the image tag in docker-compose.yml
```

or, with `docker run`:

```bash
docker pull ab0tcom/acp:v0.2.5
docker rm -f coordd
docker run -d --name coordd --restart on-failure:5 -p 8443:8443 -v acp-data:/data ab0tcom/acp:v0.2.5
```

Pin a version (`ab0tcom/acp:v0.2.5`) in production and bump deliberately. Read the release notes in
[CHANGELOG.md](../CHANGELOG.md) first: v0.2.4 rewrites the logins file in a form older versions refuse, and keeps a
backup beside it for rolling back.

## 7. Cluster (beta)

For a multi-node Raft cluster, each node runs its own container with `-raft-addr`, `-node-id`, and peer flags, and
node-to-node traffic uses cluster-CA mTLS. The container model is the same (one volume per node). Cluster mode is
**beta**: one server plus backups is the supported default. The flags are covered by the **acp-cluster** skill
and [OPERATIONS.md](OPERATIONS.md).

## 8. Build it yourself (from the released binaries)

The image does **not** build from source. The `Dockerfile` in this repo installs the **released,
checksum-verified binaries** (the same tarballs `install.sh` fetches). That means you can read the
`Dockerfile` and rebuild the *exact* official image using only public artifacts:

```bash
docker build --build-arg VERSION=v0.2.5 -t acp:local .
```

It fetches `acp_<version>_linux_<arch>.tar.gz` from the release downloads and verifies its SHA-256
against `checksums.txt` before installing; if the checksum doesn't match, the build fails. Same
chain of trust as `install.sh`; no source, no compiler in the image.

Database storage: build the server variant, which runs `coordd-server`:

```bash
docker build --build-arg VERSION=v0.2.5 --build-arg ARCHIVE=acp-server --build-arg DAEMON=coordd-server \
  --build-arg BINS="coordd-server acp-server acp-mcp-server" -t acpdb:local .
```

## Troubleshooting

- **Where is my admin token?** In the volume at `/data/owner.token`: `docker exec coordd cat /data/owner.token`
  (Compose: `docker compose exec -T acp cat /data/owner.token`). Every start logs its path and the connect line.
- **Client TLS error / "the server's certificate does not cover …"** → the certificate doesn't list the address
  you connect by. Connect by one it lists, or re-make the certificate with the right `-hosts` (section 3; a new
  `-hosts` alone does nothing on an existing volume).
- **"the daemon's certificate isn't trusted"** → the profile has no `pin`: add `pin=sha256:…` from the log, or
  join with an invite.
- **`401 invalid or missing bearer token`** → the token is wrong or was revoked. Check `token_file` with
  `acp status`. A server that switched from one shared key to one login per agent no longer accepts the shared key:
  use your own token file, or ask the admin for an invite.
- **`acp invite`: "this server gives every agent one shared token"** → the volume was started with one shared key
  (an older volume, or `-shared-token`). See the first row of "When something goes wrong" in [RUN-ACP.md](RUN-ACP.md).
- **Permission denied in `/data`** → usually a host folder mounted instead of a named volume (the image runs as
  user 10001). Use a named volume.
- **State gone after `docker rm`** → you didn't mount `-v ...:/data`; always use a volume.
- **Health shows fewer capabilities than expected** → some are opt-in flags (e.g. `-awareness-ws`).
