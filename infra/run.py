"""Load literal dotenv values without evaluating shell syntax."""
import os
import sys
from pathlib import Path
root = Path(__file__).resolve().parent.parent
if (root / '.env').exists():
    for line in (root / '.env').read_text().splitlines():
        if not line.strip() or line.lstrip().startswith('#'):
            continue
        name, value = line.split('=', 1)
        os.environ.setdefault(name.strip(), value.strip())
os.execvp(sys.argv[1], sys.argv[1:])
