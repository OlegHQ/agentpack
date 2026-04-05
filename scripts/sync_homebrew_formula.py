#!/usr/bin/env python3
"""Download tagged source tarball and refresh url/sha256 in the tap formula."""
from __future__ import annotations

import hashlib
import pathlib
import re
import sys
import urllib.error
import urllib.request


def usage() -> None:
    sys.stderr.write(
        "usage: sync_homebrew_formula.py <tap-root> <github-owner/repo> <version>\n"
        "  e.g. sync_homebrew_formula.py ../homebrew-agentpack OlegHQ/agentpack 0.2.0\n"
    )


def main() -> None:
    if len(sys.argv) != 4:
        usage()
        sys.exit(2)
    tap_root = pathlib.Path(sys.argv[1]).resolve()
    slug = sys.argv[2].strip().strip("/")
    version = sys.argv[3].strip()
    if "/" not in slug:
        sys.stderr.write("error: owner/repo required\n")
        sys.exit(2)
    tag = f"v{version}"
    url = f"https://github.com/{slug}/archive/refs/tags/{tag}.tar.gz"
    formula = tap_root / "Formula" / "agentpack.rb"
    if not formula.is_file():
        sys.stderr.write(
            f"error: missing {formula} — clone the tap or run tap-bootstrap (see README)\n"
        )
        sys.exit(1)
    try:
        with urllib.request.urlopen(url, timeout=120) as resp:
            data = resp.read()
    except urllib.error.HTTPError as e:
        sys.stderr.write(
            f"error: could not fetch {url} ({e.code})\n"
            "hint: push the git tag and wait a few seconds, then retry\n"
        )
        sys.exit(1)
    sha = hashlib.sha256(data).hexdigest()
    text = formula.read_text(encoding="utf-8")
    text2, n_url = re.subn(r'^  url "[^"]*"', f'  url "{url}"', text, count=1, flags=re.MULTILINE)
    text3, n_sha = re.subn(
        r'^  sha256 "[^"]*"',
        f'  sha256 "{sha}"',
        text2,
        count=1,
        flags=re.MULTILINE,
    )
    if n_url != 1 or n_sha != 1:
        sys.stderr.write(
            "error: formula must contain exactly one top-level `url` and `sha256` line "
            f"(got url={n_url}, sha256={n_sha})\n"
        )
        sys.exit(1)
    formula.write_text(text3, encoding="utf-8")
    print(f"updated {formula}")
    print(f"  url {url}")
    print(f"  sha256 {sha}")


if __name__ == "__main__":
    main()
