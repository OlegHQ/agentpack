#!/usr/bin/env python3
"""Bump semver in root Cargo.toml (first version = line only)."""
import pathlib
import re
import sys

KIND_MINOR = "minor"
KIND_PATCH = "patch"


def main() -> None:
    if len(sys.argv) != 2 or sys.argv[1] not in (KIND_MINOR, KIND_PATCH):
        sys.stderr.write("usage: bump_version.py minor|patch\n")
        sys.exit(2)
    kind = sys.argv[1]
    path = pathlib.Path("Cargo.toml")
    text = path.read_text(encoding="utf-8")
    m = re.search(r'^version = "(\d+)\.(\d+)\.(\d+)"', text, re.MULTILINE)
    if not m:
        sys.stderr.write("error: no version = \"x.y.z\" in Cargo.toml\n")
        sys.exit(1)
    major, minor, patch = (int(m.group(1)), int(m.group(2)), int(m.group(3)))
    if kind == KIND_MINOR:
        new_ver = f"{major}.{minor + 1}.0"
    else:
        new_ver = f"{major}.{minor}.{patch + 1}"
    updated, n = re.subn(
        r'^version = "\d+\.\d+\.\d+"',
        f'version = "{new_ver}"',
        text,
        count=1,
        flags=re.MULTILINE,
    )
    if n != 1:
        sys.stderr.write("error: failed to replace version line\n")
        sys.exit(1)
    path.write_text(updated, encoding="utf-8")
    print(new_ver)


if __name__ == "__main__":
    main()
