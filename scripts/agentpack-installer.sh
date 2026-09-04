#!/bin/sh
set -eu

APP_NAME=agentpack
APP_VERSION=${AGENTPACK_VERSION:-0.3.18}
REPOSITORY=${AGENTPACK_REPOSITORY:-OlegHQ/agentpack}

usage() {
    cat <<EOF
agentpack-installer.sh

Download, verify, and install agentpack ${APP_VERSION}.

Usage: agentpack-installer.sh [-q|--quiet] [-h|--help]

Environment:
  AGENTPACK_VERSION          release version (default: ${APP_VERSION})
  AGENTPACK_DOWNLOAD_URL     release asset base URL
  AGENTPACK_INSTALL_DIR      binary destination (default: $HOME/.local/bin)
  AGENTPACK_GITHUB_TOKEN     token for private GitHub/GHE downloads
EOF
}

quiet=${AGENTPACK_PRINT_QUIET:-${INSTALLER_PRINT_QUIET:-0}}
for argument in "$@"; do
    case "$argument" in
        -h|--help) usage; exit 0 ;;
        -q|--quiet) quiet=1 ;;
        *) echo "agentpack installer: unknown option: $argument" >&2; exit 2 ;;
    esac
done

say() {
    if [ "$quiet" != "1" ]; then
        printf '%s\n' "$*"
    fi
}

fail() {
    printf 'agentpack installer: %s\n' "$*" >&2
    exit 1
}

for command in uname mktemp mkdir mv chmod awk; do
    command -v "$command" >/dev/null 2>&1 || fail "required command not found: $command"
done

case "$(uname -s)" in
    Darwin) os=darwin; archive_format=tar ;;
    Linux) os=linux; archive_format=tar ;;
    CYGWIN*|MSYS*|MINGW*) os=windows; archive_format=zip ;;
    *) fail "unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) fail "unsupported architecture: $(uname -m)" ;;
esac
if [ "$os" = windows ]; then
    arch=amd64
    archive="${APP_NAME}_${APP_VERSION}_${os}_${arch}.zip"
    command -v unzip >/dev/null 2>&1 || fail "required command not found: unzip"
else
    archive="${APP_NAME}_${APP_VERSION}_${os}_${arch}.tar.gz"
    command -v tar >/dev/null 2>&1 || fail "required command not found: tar"
fi

if [ -n "${AGENTPACK_DOWNLOAD_URL:-}" ]; then
    base_url=${AGENTPACK_DOWNLOAD_URL%/}
elif [ -n "${INSTALLER_DOWNLOAD_URL:-}" ]; then
    base_url=${INSTALLER_DOWNLOAD_URL%/}
elif [ -n "${AGENTPACK_INSTALLER_GHE_BASE_URL:-}" ]; then
    base_url="${AGENTPACK_INSTALLER_GHE_BASE_URL%/}/${REPOSITORY}/releases/download/v${APP_VERSION}"
elif [ -n "${AGENTPACK_INSTALLER_GITHUB_BASE_URL:-}" ]; then
    base_url="${AGENTPACK_INSTALLER_GITHUB_BASE_URL%/}/${REPOSITORY}/releases/download/v${APP_VERSION}"
else
    base_url="https://github.com/${REPOSITORY}/releases/download/v${APP_VERSION}"
fi

if [ -n "${AGENTPACK_INSTALL_DIR:-}" ]; then
    install_dir=$AGENTPACK_INSTALL_DIR
else
    [ -n "${HOME:-}" ] || fail "HOME is unset; set AGENTPACK_INSTALL_DIR"
    install_dir="$HOME/.local/bin"
fi

temporary=$(mktemp -d "${TMPDIR:-/tmp}/agentpack-install.XXXXXX")
trap 'rm -rf "$temporary"' EXIT HUP INT TERM

download() {
    source_url=$1
    destination=$2
    if command -v curl >/dev/null 2>&1; then
        curl_protocol_args="--proto =https --tlsv1.2"
        case "$source_url" in
            http://127.0.0.1:*|http://localhost:*) curl_protocol_args= ;;
        esac
        if [ -n "${AGENTPACK_GITHUB_TOKEN:-}" ]; then
            # shellcheck disable=SC2086 # two intentional curl arguments
            curl $curl_protocol_args -fsSL -H "Authorization: Bearer ${AGENTPACK_GITHUB_TOKEN}" "$source_url" -o "$destination"
        else
            # shellcheck disable=SC2086 # two intentional curl arguments
            curl $curl_protocol_args -fsSL "$source_url" -o "$destination"
        fi
    elif command -v wget >/dev/null 2>&1; then
        if [ -n "${AGENTPACK_GITHUB_TOKEN:-}" ]; then
            wget -q --header="Authorization: Bearer ${AGENTPACK_GITHUB_TOKEN}" -O "$destination" "$source_url"
        else
            wget -q -O "$destination" "$source_url"
        fi
    else
        fail "curl or wget is required"
    fi
}

say "Downloading agentpack ${APP_VERSION} (${os}/${arch})"
download "$base_url/$archive" "$temporary/$archive"
download "$base_url/checksums.txt" "$temporary/checksums.txt"
expected=$(awk -v name="$archive" '$2 == name || $2 == "*" name { print $1; exit }' "$temporary/checksums.txt")
[ -n "$expected" ] || fail "checksum for $archive is missing"
if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$temporary/$archive" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
    actual=$(shasum -a 256 "$temporary/$archive" | awk '{print $1}')
elif command -v openssl >/dev/null 2>&1; then
    actual=$(openssl dgst -sha256 "$temporary/$archive" | awk '{print $NF}')
else
    fail "sha256sum, shasum, or openssl is required"
fi
[ "$actual" = "$expected" ] || fail "checksum mismatch for $archive"

if [ "$archive_format" = zip ]; then
    unzip -q "$temporary/$archive" -d "$temporary/unpacked"
    binary_name=agentpack.exe
else
    mkdir -p "$temporary/unpacked"
    tar -xzf "$temporary/$archive" -C "$temporary/unpacked"
    binary_name=agentpack
fi
[ -f "$temporary/unpacked/$binary_name" ] || fail "$binary_name is missing from $archive"
mkdir -p "$install_dir"
chmod 755 "$temporary/unpacked/$binary_name"
mv "$temporary/unpacked/$binary_name" "$install_dir/$binary_name.new"
mv "$install_dir/$binary_name.new" "$install_dir/$binary_name"

say "Installed agentpack ${APP_VERSION} to $install_dir/$binary_name"
