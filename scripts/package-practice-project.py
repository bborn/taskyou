#!/usr/bin/env python3
"""Build the downloadable practice project from its canonical example files."""
from pathlib import Path
from zipfile import ZipFile, ZipInfo, ZIP_DEFLATED

root = Path(__file__).resolve().parent.parent
out = root / 'docs/downloads/taskyou-storefront.zip'
out.parent.mkdir(parents=True, exist_ok=True)
with ZipFile(out, 'w', compression=ZIP_DEFLATED) as archive:
    for name in ('README.md', 'index.html', 'style.css', 'start.sh'):
        info = ZipInfo('taskyou-storefront/' + name, (2026, 1, 1, 0, 0, 0))
        info.compress_type = ZIP_DEFLATED
        info.external_attr = (0o100755 if name.endswith('.sh') else 0o100644) << 16
        archive.writestr(info, (root / 'examples/storefront' / name).read_bytes())
print(out.relative_to(root))
