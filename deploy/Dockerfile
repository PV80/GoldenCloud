# =============================================================================
# GoldenCloud server — container image
# =============================================================================
#
# The supported production deployment is the systemd service described in
# deploy/RUNBOOK.md. This image exists for two other cases:
#
#   * running the server on a mini-PC or NAS that already runs Docker;
#   * running a throwaway server for the client test in docs/CLIENT-TEST.md.
#
# BUILD CONTEXT IS THE REPOSITORY ROOT, not this directory:
#
#     docker build -f deploy/Dockerfile -t goldencloud:local .
#
# Cross-building for a Raspberry Pi from an x86 laptop:
#
#     docker buildx build --platform linux/arm64 \
#       -f deploy/Dockerfile -t goldencloud:local --load .
#
# =============================================================================

# -----------------------------------------------------------------------------
# Stage 1 — build
# -----------------------------------------------------------------------------
# Pinned to the Go version CI uses (.github/workflows/ci.yml, GO_VERSION).
# Alpine rather than Debian purely because it pulls less; nothing from it ends
# up in the final image.
FROM golang:1.25-alpine AS build

WORKDIR /src

# Copy the module files first and download dependencies as their own layer, so
# that editing a .go file does not re-download the whole module cache.
COPY server/go.mod server/go.sum ./
RUN go mod download

# Now the source.
COPY server/ ./

# TARGETOS/TARGETARCH are supplied automatically by buildx. The defaults make a
# plain `docker build` work too.
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev

# CGO_ENABLED=0 is the whole point: it produces a genuinely static binary with
# no libc dependency, which is what lets the final stage be scratch-like.
# -trimpath strips local build paths out of the binary (reproducibility, and no
# leaking of the builder's directory layout).
# -s -w drops the symbol table and DWARF debug info; roughly halves the size.
ENV CGO_ENABLED=0
RUN GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/goldencloud \
      ./cmd/goldencloud

# Fail the build loudly if the binary turned out dynamically linked, which
# would mean CGO crept back in and the final image would not run.
RUN ! ldd /out/goldencloud 2>/dev/null | grep -q "=>" || \
    (echo "ERROR: binary is dynamically linked; CGO_ENABLED=0 was not honoured" && exit 1)

# -----------------------------------------------------------------------------
# Stage 2 — runtime
# -----------------------------------------------------------------------------
# distroless/static-debian12 contains: CA certificates, /etc/passwd with a
# nonroot user, timezone data, and nothing else. No shell, no package manager,
# no coreutils. An attacker who achieves code execution has no tools to use.
#
# The :nonroot tag runs as UID 65532 by default.
FROM gcr.io/distroless/static-debian12:nonroot

# OCI labels so `docker inspect` tells you what this is.
LABEL org.opencontainers.image.title="GoldenCloud server"
LABEL org.opencontainers.image.description="Private per-user WebDAV drive"
LABEL org.opencontainers.image.source="https://github.com/PV80/GoldenCloud"
LABEL org.opencontainers.image.licenses="MIT"

COPY --from=build /out/goldencloud /usr/local/bin/goldencloud

# UID 65532, GID 65532 — the distroless "nonroot" account. Anything the server
# must write to (the storage root) has to be writable by this UID on the host.
# See docker-compose.example.yml for how that is arranged.
USER 65532:65532

# Loopback inside the container is not loopback on the host, so a container
# deployment must set `listen: "0.0.0.0:8080"` in its config AND put a TLS
# terminator in front. Read the trusted_proxy notes in goldencloud.example.yaml
# before you do that.
EXPOSE 8080

# Config and user database are mounted read-only from the host.
VOLUME ["/etc/goldencloud"]
# Staff files.
VOLUME ["/srv/goldencloud"]

ENTRYPOINT ["/usr/local/bin/goldencloud"]
CMD ["serve", "--config", "/etc/goldencloud/config.yaml"]
