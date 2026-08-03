#!/usr/bin/env bash
#
# GoldenCloud server installer — the shortcut for sections 8 to 11 (steps 75 to
# 102) of RUNBOOK.md.
#
# What it does, idempotently (safe to run again after a failure or an upgrade):
#
#   1. Checks it is running as root on a supported 64-bit Linux machine.
#   2. Works out whether this is an arm64 or an amd64 box.
#   3. Downloads the matching goldencloud-server binary from a GitHub Release,
#      verifies its SHA-256 checksum, and installs it to /usr/local/bin.
#   4. Creates the `goldencloud` system account if it does not exist.
#   5. Creates /etc/goldencloud, an empty users.yaml, and a config.yaml seeded
#      from goldencloud.example.yaml — WITHOUT overwriting an existing config.
#   6. Creates the storage root and gives it to the goldencloud account.
#   7. Installs and enables the systemd unit.
#
# What it deliberately does NOT do, because each needs a decision from you:
#
#   * Mount the WD share (RUNBOOK.md section 7 — needs your NAS address and
#     password).
#   * Create staff users (RUNBOOK.md section 12).
#   * Set up the Cloudflare Tunnel (RUNBOOK.md section 13 — needs a browser
#     sign-in to your Cloudflare account).
#
# Usage:
#
#   sudo ./install.sh                       # latest release
#   sudo ./install.sh --version v0.2.0      # a specific release
#   sudo ./install.sh --binary ./goldencloud-server-linux-arm64
#                                           # a file you already downloaded
#   sudo ./install.sh --storage-root /mnt/wd/goldencloud
#   sudo ./install.sh --dry-run             # print what it would do, change nothing
#
set -euo pipefail

# ---------------------------------------------------------------------------
# Defaults
# ---------------------------------------------------------------------------
REPO="PV80/GoldenCloud"
VERSION="latest"
BINARY_PATH=""
STORAGE_ROOT="/mnt/wd/goldencloud"
CONFIG_DIR="/etc/goldencloud"
INSTALL_PATH="/usr/local/bin/goldencloud"
UNIT_PATH="/etc/systemd/system/goldencloud.service"
SERVICE_USER="goldencloud"
DRY_RUN="no"
SKIP_SERVICE="no"

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

# ---------------------------------------------------------------------------
# Output helpers
# ---------------------------------------------------------------------------
if [[ -t 1 ]]; then
    C_RESET=$'\033[0m'; C_BOLD=$'\033[1m'
    C_RED=$'\033[31m';  C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'; C_BLUE=$'\033[34m'
else
    C_RESET=""; C_BOLD=""; C_RED=""; C_GREEN=""; C_YELLOW=""; C_BLUE=""
fi

step()  { printf '%s==>%s %s%s%s\n' "$C_BLUE" "$C_RESET" "$C_BOLD" "$1" "$C_RESET"; }
ok()    { printf '    %s[ ok ]%s %s\n'   "$C_GREEN"  "$C_RESET" "$1"; }
skip()  { printf '    %s[skip]%s %s\n'   "$C_YELLOW" "$C_RESET" "$1"; }
info()  { printf '    %s\n' "$1"; }
warn()  { printf '%swarning:%s %s\n' "$C_YELLOW" "$C_RESET" "$1" >&2; }
die()   { printf '%serror:%s %s\n'   "$C_RED"    "$C_RESET" "$1" >&2; exit 1; }

run() {
    if [[ "$DRY_RUN" == "yes" ]]; then
        printf '    would run: %s\n' "$*"
    else
        "$@"
    fi
}

usage() {
    sed -n '2,33p' "${BASH_SOURCE[0]}" | sed 's/^#\{0,1\} \{0,1\}//'
    exit 0
}

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
    case "$1" in
        --version)      VERSION="${2:-}";      shift 2 ;;
        --binary)       BINARY_PATH="${2:-}";  shift 2 ;;
        --storage-root) STORAGE_ROOT="${2:-}"; shift 2 ;;
        --repo)         REPO="${2:-}";         shift 2 ;;
        --dry-run)      DRY_RUN="yes";         shift   ;;
        --skip-service) SKIP_SERVICE="yes";    shift   ;;
        -h|--help)      usage ;;
        *)              die "unknown option: $1 (try --help)" ;;
    esac
done

[[ -n "$VERSION" ]]      || die "--version needs a value, e.g. v0.1.0 or latest"
[[ -n "$STORAGE_ROOT" ]] || die "--storage-root needs a value"

printf '\n%sGoldenCloud server installer%s\n\n' "$C_BOLD" "$C_RESET"
if [[ "$DRY_RUN" == "yes" ]]; then
    warn "dry run — nothing will be changed"
fi

# ---------------------------------------------------------------------------
# 1. Preflight
# ---------------------------------------------------------------------------
step "Checking this machine"

if [[ "${EUID}" -ne 0 ]]; then
    die "this script must run as root. Re-run it as:  sudo $0 $*"
fi
ok "running as root"

[[ "$(uname -s)" == "Linux" ]] || die "this installer is for Linux only (found $(uname -s))"
ok "operating system is Linux"

MACHINE="$(uname -m)"
case "$MACHINE" in
    aarch64|arm64) ARCH="arm64" ;;
    x86_64|amd64)  ARCH="amd64" ;;
    armv7l|armv6l)
        die "this is a 32-bit ARM system ($MACHINE). GoldenCloud needs 64-bit
       Raspberry Pi OS. Re-flash the SD card with the 64-bit image — see
       RUNBOOK.md section 2." ;;
    *)
        die "unsupported CPU architecture: $MACHINE
       Only arm64 (Raspberry Pi 5) and amd64 (Intel/AMD) are released." ;;
esac
ok "architecture is $MACHINE, will install the linux-$ARCH build"

for tool in install useradd systemctl sha256sum; do
    command -v "$tool" >/dev/null 2>&1 || die "required command not found: $tool"
done
ok "required system tools present"

if [[ -z "$BINARY_PATH" ]]; then
    command -v curl >/dev/null 2>&1 || die "curl is not installed. Run:  sudo apt install -y curl"
fi

if ! command -v systemctl >/dev/null 2>&1; then
    die "this system does not use systemd; follow RUNBOOK.md manually"
fi

# ---------------------------------------------------------------------------
# 2. Obtain the binary
# ---------------------------------------------------------------------------
step "Obtaining the goldencloud binary"

WORKDIR=""
cleanup() {
    if [[ -n "$WORKDIR" && -d "$WORKDIR" ]]; then
        rm -rf -- "$WORKDIR"
    fi
}
trap cleanup EXIT

ASSET="goldencloud-server-linux-${ARCH}"

if [[ -n "$BINARY_PATH" ]]; then
    [[ -f "$BINARY_PATH" ]] || die "no such file: $BINARY_PATH"
    STAGED="$BINARY_PATH"
    ok "using local file $BINARY_PATH"
    if [[ -f "${BINARY_PATH}.sha256" ]]; then
        if ( cd -- "$(dirname -- "$BINARY_PATH")" && sha256sum -c --status "$(basename -- "$BINARY_PATH").sha256" ); then
            ok "checksum verified against ${BINARY_PATH}.sha256"
        else
            die "CHECKSUM MISMATCH for $BINARY_PATH — do not install this file"
        fi
    else
        warn "no ${BINARY_PATH}.sha256 next to it; skipping checksum verification"
    fi
else
    WORKDIR="$(mktemp -d)"
    if [[ "$VERSION" == "latest" ]]; then
        BASE_URL="https://github.com/${REPO}/releases/latest/download"
    else
        BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
    fi
    info "downloading from ${BASE_URL}"

    if [[ "$DRY_RUN" == "yes" ]]; then
        printf '    would download: %s and %s.sha256\n' "$ASSET" "$ASSET"
        STAGED="$WORKDIR/$ASSET"
        : >"$STAGED"
    else
        curl -fL --retry 3 --retry-delay 2 -o "$WORKDIR/$ASSET" \
             "${BASE_URL}/${ASSET}" \
            || die "download failed. Check the machine has internet access and that
       release '${VERSION}' exists at https://github.com/${REPO}/releases"
        curl -fL --retry 3 --retry-delay 2 -o "$WORKDIR/${ASSET}.sha256" \
             "${BASE_URL}/${ASSET}.sha256" \
            || die "downloaded the binary but not its .sha256 checksum file.
       Refusing to install an unverified binary."
        ok "downloaded $ASSET"

        if ( cd -- "$WORKDIR" && sha256sum -c --status "${ASSET}.sha256" ); then
            ok "SHA-256 checksum matches"
        else
            die "CHECKSUM MISMATCH. The download is corrupt or has been tampered
       with. Nothing has been installed. Delete it and try again."
        fi
        STAGED="$WORKDIR/$ASSET"
    fi
fi

# ---------------------------------------------------------------------------
# 3. Install the binary
# ---------------------------------------------------------------------------
step "Installing $INSTALL_PATH"

if [[ -x "$INSTALL_PATH" && "$DRY_RUN" != "yes" ]]; then
    OLD_VERSION="$("$INSTALL_PATH" version 2>/dev/null || echo "unknown")"
    info "replacing existing install (was: ${OLD_VERSION})"
fi

# `install` writes to a temp name and renames, so a running server is never
# reading a half-written file.
run install -o root -g root -m 0755 "$STAGED" "$INSTALL_PATH"
ok "installed to $INSTALL_PATH"

if [[ "$DRY_RUN" != "yes" ]]; then
    NEW_VERSION="$("$INSTALL_PATH" version 2>&1 || true)"
    info "version reports: ${NEW_VERSION}"
fi

# ---------------------------------------------------------------------------
# 4. Service account
# ---------------------------------------------------------------------------
step "Creating the $SERVICE_USER system account"

if getent group "$SERVICE_USER" >/dev/null 2>&1; then
    skip "group $SERVICE_USER already exists"
else
    run groupadd --system "$SERVICE_USER"
    ok "created group $SERVICE_USER"
fi

if id -u "$SERVICE_USER" >/dev/null 2>&1; then
    skip "user $SERVICE_USER already exists"
else
    run useradd --system \
                --gid "$SERVICE_USER" \
                --home-dir /nonexistent \
                --no-create-home \
                --shell /usr/sbin/nologin \
                --comment "GoldenCloud WebDAV server" \
                "$SERVICE_USER"
    ok "created user $SERVICE_USER (no login shell, no home directory)"
fi

# ---------------------------------------------------------------------------
# 5. Configuration
# ---------------------------------------------------------------------------
step "Setting up $CONFIG_DIR"

run install -o root -g "$SERVICE_USER" -m 0750 -d "$CONFIG_DIR"
ok "$CONFIG_DIR exists, owned root:$SERVICE_USER, mode 0750"

CONFIG_FILE="$CONFIG_DIR/config.yaml"
EXAMPLE_FILE="$SCRIPT_DIR/goldencloud.example.yaml"

if [[ -f "$CONFIG_FILE" ]]; then
    skip "$CONFIG_FILE already exists — left untouched"
    info "if you want the new example, compare with $EXAMPLE_FILE"
elif [[ -f "$EXAMPLE_FILE" ]]; then
    run install -o root -g "$SERVICE_USER" -m 0640 "$EXAMPLE_FILE" "$CONFIG_FILE"
    if [[ "$DRY_RUN" != "yes" ]]; then
        # Point the fresh config at the storage root the operator asked for.
        sed -i -E "s#^storage_root:.*#storage_root: \"${STORAGE_ROOT}\"#" "$CONFIG_FILE"
    fi
    ok "created $CONFIG_FILE with storage_root: $STORAGE_ROOT"
    warn "READ $CONFIG_FILE BEFORE GOING LIVE. It is a starting point, not a finished config."
else
    warn "$EXAMPLE_FILE not found; no config.yaml created."
    warn "Create $CONFIG_FILE by hand — see RUNBOOK.md section 9."
fi

USERS_FILE="$CONFIG_DIR/users.yaml"
if [[ -f "$USERS_FILE" ]]; then
    skip "$USERS_FILE already exists — left untouched (it holds your users)"
else
    if [[ "$DRY_RUN" == "yes" ]]; then
        printf '    would create empty %s\n' "$USERS_FILE"
    else
        printf 'users: []\n' >"$USERS_FILE"
        chown "root:$SERVICE_USER" "$USERS_FILE"
        chmod 0640 "$USERS_FILE"
    fi
    ok "created empty $USERS_FILE"
fi

# ---------------------------------------------------------------------------
# 6. Storage root
# ---------------------------------------------------------------------------
step "Preparing the storage root $STORAGE_ROOT"

STORAGE_PARENT="$(dirname -- "$STORAGE_ROOT")"
if ! mountpoint -q "$STORAGE_PARENT" 2>/dev/null; then
    warn "$STORAGE_PARENT is not a mount point."
    warn "If the WD share is meant to be mounted there, STOP and do RUNBOOK.md"
    warn "section 7 first. Creating folders here would put staff files on the"
    warn "SD card instead of the NAS, and they would vanish when the share came back."
fi

run install -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0750 -d "$STORAGE_ROOT"
ok "$STORAGE_ROOT exists, owned $SERVICE_USER:$SERVICE_USER, mode 0750"

# ---------------------------------------------------------------------------
# 7. systemd unit
# ---------------------------------------------------------------------------
if [[ "$SKIP_SERVICE" == "yes" ]]; then
    step "Skipping systemd unit (--skip-service)"
else
    step "Installing the systemd service"

    UNIT_SRC="$SCRIPT_DIR/goldencloud.service"
    [[ -f "$UNIT_SRC" ]] || die "$UNIT_SRC not found — run this script from the deploy/ directory of the repository"

    run install -o root -g root -m 0644 "$UNIT_SRC" "$UNIT_PATH"
    ok "installed $UNIT_PATH"

    run systemctl daemon-reload
    ok "systemd reloaded"

    run systemctl enable goldencloud
    ok "goldencloud enabled — it will start automatically at every boot"

    if [[ "$DRY_RUN" != "yes" ]]; then
        if systemctl is-active --quiet goldencloud; then
            info "restarting the already-running service"
            systemctl restart goldencloud
        else
            systemctl start goldencloud || true
        fi
        sleep 2
        if systemctl is-active --quiet goldencloud; then
            ok "goldencloud is running"
        else
            warn "goldencloud did not start. This is expected if the WD share is"
            warn "not mounted yet or config.yaml still needs editing. Diagnose with:"
            warn "    sudo systemctl status goldencloud"
            warn "    sudo journalctl -u goldencloud -n 50 --no-pager"
        fi
    fi
fi

# ---------------------------------------------------------------------------
# Done
# ---------------------------------------------------------------------------
printf '\n%sInstalled.%s What is left to do:\n\n' "$C_GREEN" "$C_RESET"
cat <<EOF
  1. Check the config is right for you:
         sudo nano $CONFIG_FILE

  2. Create your staff users (RUNBOOK.md section 12):
         sudo goldencloud user add --config $CONFIG_FILE alice
         sudo goldencloud user list --config $CONFIG_FILE

  3. Confirm the server answers locally — 401 is the correct, healthy answer:
         curl -sS -o /dev/null -w '%{http_code}\\n' http://127.0.0.1:8080/

  4. Publish it with Cloudflare Tunnel (RUNBOOK.md section 13).

  Watch the log at any time with:
         sudo journalctl -u goldencloud -f
EOF
printf '\n'
