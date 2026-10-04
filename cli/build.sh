#!/bin/bash
#
# Cross-platform build script for steer CLI.
# Outputs binaries into ../bin/<os>-<arch>/steer[.exe]
#
# Usage:
#   ./build.sh                    # build all platforms
#   ./build.sh --install          # build all + copy current platform to /usr/local/bin
#   VERSION=1.2.0 ./build.sh     # set version

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_DIR="$(dirname "$SCRIPT_DIR")"
BIN_DIR="${PLUGIN_DIR}/bin"

# Version info
VERSION="${VERSION:-dev}"
GIT_COMMIT="$(git -C "$SCRIPT_DIR" rev-parse --short HEAD 2>/dev/null || echo "unknown")"
BUILD_DATE="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

LDFLAGS="-s -w"
LDFLAGS="$LDFLAGS -X 'github.com/steer-sh/steer-cli/internal/version.Version=${VERSION}'"
LDFLAGS="$LDFLAGS -X 'github.com/steer-sh/steer-cli/internal/version.GitCommit=${GIT_COMMIT}'"
LDFLAGS="$LDFLAGS -X 'github.com/steer-sh/steer-cli/internal/version.BuildDate=${BUILD_DATE}'"

echo "Building steer ${VERSION} (${GIT_COMMIT})..."
echo ""

# Clean previous builds
rm -rf "${BIN_DIR}"

build_target() {
    local os="$1" arch="$2" dir_name="$3" binary="$4"

    local target_dir="${BIN_DIR}/${dir_name}"
    mkdir -p "$target_dir"

    echo -n "  ${dir_name}..."
    GOOS="$os" GOARCH="$arch" go build \
        -C "$SCRIPT_DIR" \
        -ldflags="${LDFLAGS}" \
        -o "${target_dir}/${binary}" .
    echo " done"
}

build_target darwin  amd64 darwin-amd64   steer
build_target darwin  arm64 darwin-arm64   steer
build_target linux   amd64 linux-amd64    steer
build_target linux   arm64 linux-arm64    steer

echo ""
echo "Binaries written to bin/"
ls -1d "${BIN_DIR}"/*/

# --install: copy current-platform binary to /usr/local/bin
if [[ "${1:-}" == "--install" ]]; then
    OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
    ARCH="$(uname -m)"
    case "$ARCH" in
        x86_64)  ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
    esac

    SRC="${BIN_DIR}/${OS}-${ARCH}/steer"
    if [[ ! -f "$SRC" ]]; then
        echo "ERROR: no binary for ${OS}-${ARCH}" >&2
        exit 1
    fi

    echo ""
    echo "Installing ${OS}-${ARCH}/steer -> /usr/local/bin/steer"
    sudo cp "$SRC" /usr/local/bin/steer
    sudo chmod +x /usr/local/bin/steer
    echo "Installed: $(steer --help 2>&1 | head -1 || echo 'steer')"
fi
