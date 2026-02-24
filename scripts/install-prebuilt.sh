#!/usr/bin/env bash
#
# Install steer from the prebuilt binary shipped in this repository.
#
# Usage:
#   bash scripts/install-prebuilt.sh                # install to ~/.local/bin/steer
#   bash scripts/install-prebuilt.sh --user         # same as default
#   bash scripts/install-prebuilt.sh --system       # install to /usr/local/bin/steer
#   bash scripts/install-prebuilt.sh --dest /path/to/steer

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_DIR="$(dirname "$SCRIPT_DIR")"
BIN_DIR="${PLUGIN_DIR}/bin"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *)
        echo "steer install: unsupported architecture: $ARCH" >&2
        exit 1
        ;;
esac

SRC="${BIN_DIR}/${OS}-${ARCH}/steer"
if [[ ! -f "$SRC" ]]; then
    echo "steer install: no prebuilt binary found for ${OS}-${ARCH} at ${SRC}" >&2
    echo "If you are on a supported platform, refresh this checkout. As a fallback run:" >&2
    echo "  cd ${PLUGIN_DIR}/cli && bash build.sh" >&2
    exit 1
fi

MODE="user"
DEST=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --user)
            MODE="user"
            shift
            ;;
        --system)
            MODE="system"
            shift
            ;;
        --dest)
            if [[ $# -lt 2 ]]; then
                echo "steer install: --dest requires a path" >&2
                exit 1
            fi
            DEST="$2"
            shift 2
            ;;
        -h|--help)
            sed -n '1,12p' "$0"
            exit 0
            ;;
        *)
            echo "steer install: unknown argument: $1" >&2
            exit 1
            ;;
    esac
done

if [[ -z "$DEST" ]]; then
    if [[ "$MODE" == "system" ]]; then
        DEST="/usr/local/bin/steer"
    else
        DEST="${HOME}/.local/bin/steer"
    fi
fi

DEST_DIR="$(dirname "$DEST")"
if [[ "$MODE" == "system" ]]; then
    if [[ ! -d "$DEST_DIR" ]]; then
        if command -v sudo >/dev/null 2>&1; then
            sudo mkdir -p "$DEST_DIR"
        else
            mkdir -p "$DEST_DIR"
        fi
    fi
else
    mkdir -p "$DEST_DIR"
fi

copy_with_permissions() {
    local src="$1"
    local dest="$2"
    if cp "$src" "$dest" 2>/dev/null; then
        chmod +x "$dest"
        return 0
    fi
    if command -v sudo >/dev/null 2>&1; then
        sudo cp "$src" "$dest"
        sudo chmod +x "$dest"
        return 0
    fi
    return 1
}

if ! copy_with_permissions "$SRC" "$DEST"; then
    echo "steer install: failed to install to ${DEST}" >&2
    exit 1
fi

echo "Installed steer from ${SRC}"
echo "Destination: ${DEST}"
echo

if command -v steer >/dev/null 2>&1; then
    RESOLVED="$(command -v steer)"
    echo "steer on PATH: ${RESOLVED}"
else
    echo "steer is not currently on PATH."
fi

echo "Verification:"
"$DEST" --help >/dev/null 2>&1 && echo "  OK: ${DEST} runs successfully"
