#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
    echo 'usage: render-installers.sh VERSION OUTPUT_DIRECTORY' >&2
    exit 2
fi

version=$1
output_dir=$2
case "$version" in
    *[!a-zA-Z0-9.+-]*|'')
        echo "invalid release version: $version" >&2
        exit 2
        ;;
esac

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
mkdir -p "$output_dir"
for name in agentpack-installer.sh agentpack-installer.ps1; do
    sed "s/@AGENTPACK_VERSION@/$version/g" "$script_dir/$name" > "$output_dir/$name"
done
