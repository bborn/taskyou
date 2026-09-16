#!/usr/bin/env python3
"""Keep at most 15 MB of evidence, prioritizing results and screenshots."""
import sys
from pathlib import Path

root = Path(sys.argv[1])
files = sorted(root.glob('*'), key=lambda p: (p.suffix not in ('.json', '.png'), p.name))
remaining = 15 * 1024 * 1024
for path in files:
    if not path.is_file():
        continue
    if path.suffix == '.log' and path.stat().st_size > 1024 * 1024:
        with path.open('rb') as stream:
            stream.seek(-1024 * 1024, 2)
            tail = stream.read()
        path.write_bytes(tail)
    size = path.stat().st_size
    if size > remaining:
        path.unlink()
    else:
        remaining -= size
