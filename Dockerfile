# syntax=docker/dockerfile:1
#
# ACP — public runtime image: the coordd daemon (default) + the acp CLI + acp-mcp bridge.
#
# RULE: this image ONLY installs the RELEASED, checksum-verified public binaries (the same tarballs
# install.sh fetches). The source is PRIVATE — NEVER add a build/compile stage or COPY source here.
# If you need a source build, that is the maintainer-only acp/Dockerfile, not this file.
#
#   Pull:   docker pull ab0tcom/acp:v0.2.0        # or :latest
#   Build:  docker build --build-arg VERSION=v0.2.0 -t ab0tcom/acp:v0.2.0 .
#   Run:    docker run -p 8443:8443 -v acp-data:/data ab0tcom/acp:v0.2.0 -hosts <host>,<ip>
#   Full run/TLS/compose guide: docs/DOCKER.md
#
# Server DB mode (ACPDB): build the server variant — it runs coordd-server out of the box:
#   docker build --build-arg ARCHIVE=acp-server --build-arg DAEMON=coordd-server \
#     --build-arg BINS="coordd-server acp-server acp-mcp-server" -t ab0tcom/acp:v0.2.0-server .
#   docker run -p 8443:8443 -v acp-data:/data ab0tcom/acp:v0.2.0-server -hosts <host>,<ip>
FROM alpine:3.20
ARG VERSION=v0.2.0
ARG REPO=ab0t-com/acp
ARG ARCHIVE=acp                        # release bundle prefix: "acp" (local) | "acp-server" (server DB mode)
ARG BINS="coordd acp acp-mcp"          # local bundle → coordd acp acp-mcp; server → coordd-server acp-server acp-mcp-server
ARG DAEMON=coordd                      # daemon the entrypoint runs (server variant: coordd-server)
ARG TARGETARCH

RUN apk add --no-cache ca-certificates tzdata wget \
 && adduser -D -H -u 10001 acp \
 && mkdir -p /data && chown acp /data

# Fetch + sha256-verify the release archive (same source + checksums.txt as install.sh; no private source).
RUN set -eux; \
    arch="${TARGETARCH:-amd64}"; case "$arch" in amd64|arm64) ;; *) echo "unsupported arch: $arch" >&2; exit 1 ;; esac; \
    base="https://raw.githubusercontent.com/${REPO}/main/releases/downloads"; \
    archive="${ARCHIVE}_${VERSION}_linux_${arch}.tar.gz"; \
    wget -qO /tmp/a.tgz "${base}/${archive}"; \
    wget -qO /tmp/checksums.txt "${base}/checksums.txt"; \
    want="$(awk -v a="${archive}" '$2==a{print $1}' /tmp/checksums.txt)"; \
    have="$(sha256sum /tmp/a.tgz | awk '{print $1}')"; \
    [ -n "$want" ] && [ "$want" = "$have" ] || { echo "checksum verify FAILED for ${archive}" >&2; exit 1; }; \
    tar -xzf /tmp/a.tgz -C /tmp; \
    for b in ${BINS}; do install -m 0755 "/tmp/${b}" "/usr/local/bin/${b}"; done; \
    rm -f /tmp/a.tgz /tmp/checksums.txt

# Entrypoint runs the daemon chosen at build time (coordd | coordd-server); overridable at run via -e ACP_DAEMON=.
ENV ACP_DAEMON=${DAEMON}
RUN printf '#!/bin/sh\nexec "${ACP_DAEMON:-coordd}" "$@"\n' > /usr/local/bin/acp-entry \
 && chmod 0755 /usr/local/bin/acp-entry

USER acp
WORKDIR /data
# coordd writes its shared token + self-signed cert into /data on first run and keeps all space
# state here — mount a named volume to persist across restarts.
VOLUME ["/data"]
EXPOSE 8443
# Liveness: the no-auth health endpoint (self-signed TLS → skip verification).
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- --no-check-certificate https://127.0.0.1:8443/v1/healthz >/dev/null 2>&1 || exit 1
# For clients to trust the cert by hostname/IP, append `-hosts <host>,<ip>` so it lands in the SANs.
ENTRYPOINT ["/usr/local/bin/acp-entry"]
CMD ["-addr", ":8443", "-data", "/data"]
