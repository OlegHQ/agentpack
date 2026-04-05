#!/usr/bin/env python3
import pathlib
import re
import sys

t = pathlib.Path("Cargo.toml").read_text(encoding="utf-8")
m = re.search(r'^version = "([^"]+)"', t, re.MULTILINE)
if not m:
    sys.stderr.write("error: no version in Cargo.toml\n")
    sys.exit(1)
print(m.group(1))
