import getpass
import os
import subprocess
from pathlib import Path
root = Path(__file__).resolve().parent.parent
env = os.environ.copy()
for line in (root / '.env').read_text().splitlines():
    if line.strip() and not line.lstrip().startswith('#'):
        name, value = line.split('=', 1)
        env.setdefault(name.strip(), value.strip())
env['BOOTSTRAP_EMAIL'] = input('Admin email: ').strip()
env['BOOTSTRAP_NAME'] = input('Admin name: ').strip() or 'Admin'
env['BOOTSTRAP_PASSWORD'] = getpass.getpass('Password (12–72 bytes): ')
if env['BOOTSTRAP_PASSWORD'] != getpass.getpass('Repeat password: '):
    raise SystemExit('Passwords did not match')
subprocess.run(['go', 'run', './cmd/service', 'bootstrap'], cwd=root / 'api', env=env, check=True)
