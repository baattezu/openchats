"""Offline production backup. Encrypt with age; key management stays external."""
import datetime
import os
import subprocess
import sys
import tempfile
from pathlib import Path
root = Path(__file__).resolve().parent.parent
compose = ['docker', 'compose', '--env-file', str(root / '.env'), '-f', str(root / 'infra/compose.prod.yaml')]
def run(*args, **kwargs):
    return subprocess.run([*compose, *args], check=True, **kwargs)
mode = sys.argv[1]
if mode == 'backup':
    recipient = os.environ.get('AGE_RECIPIENT')
    if not recipient:
        raise SystemExit('Set AGE_RECIPIENT to your age public key. Install age first.')
    output = root / 'backups' / (datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ') + '.tar.age')
    output.parent.mkdir(mode=0o700, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='messaging-backup-') as temp:
        temp = Path(temp)
        run('stop', 'api')
        try:
            with (temp / 'database.dump').open('wb') as f:
                run('exec', '-T', 'db', 'pg_dump', '-U', 'messaging', '-d', 'messaging', '-Fc', stdout=f)
            run('cp', 'api:/data/media', str(temp / 'media'))
            # Encryption key is deliberately NOT bundled with the encrypted archive.
            archive = subprocess.Popen(['tar', '-C', str(temp), '-cf', '-', 'database.dump', 'media'], stdout=subprocess.PIPE)
            subprocess.run(['age', '-r', recipient, '-o', str(output)], stdin=archive.stdout, check=True)
            archive.stdout.close()
            if archive.wait() != 0:
                raise SystemExit('Archive creation failed')
        finally:
            run('start', 'api')
    output.chmod(0o600)
    print(f'Encrypted backup: {output}')
elif mode == 'restore':
    if len(sys.argv) < 3 or not os.environ.get('AGE_IDENTITY'):
        raise SystemExit('Usage: AGE_IDENTITY=/path/key make restore ARCHIVE=/path/archive.tar.age')
    if input('Restore replaces the current database/media. Type RESTORE: ') != 'RESTORE':
        raise SystemExit('Cancelled')
    with tempfile.TemporaryDirectory(prefix='messaging-restore-') as temp:
        tar = Path(temp) / 'backup.tar'
        subprocess.run(['age', '-d', '-i', os.environ['AGE_IDENTITY'], '-o', str(tar), sys.argv[2]], check=True)
        # Validate archive names before extracting.
        import tarfile
        with tarfile.open(tar) as archive:
            archive.extractall(Path(temp) / 'contents', filter='data')
        contents = Path(temp) / 'contents'
        run('stop', 'api')
        with (contents / 'database.dump').open('rb') as f:
            run('exec', '-T', 'db', 'pg_restore', '-U', 'messaging', '-d', 'messaging', '--clean', '--if-exists', stdin=f)
        run('cp', str(contents / 'media') + '/.', 'api:/data/media')
        run('run', '--rm', '--no-deps', '--user', '0', '--entrypoint', 'chown', 'api', '-R', '10001:10001', '/data/media')
        run('start', 'api')
    print('Restore complete. Verify account sessions and attachment access.')
else:
    raise SystemExit('Expected backup or restore')
