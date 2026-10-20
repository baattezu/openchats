"""Create development secrets once. Never overwrite an existing .env."""
import base64
import secrets
from pathlib import Path
root = Path(__file__).resolve().parent.parent
path = root / '.env'
if path.exists():
    print('.env exists; left unchanged')
else:
    password = secrets.token_urlsafe(24)
    text = (root / '.env.example').read_text().replace('CHANGE_ME', password).replace('BASE64_OF_32_RANDOM_BYTES', base64.b64encode(secrets.token_bytes(32)).decode()).replace('RANDOM_SECRET', secrets.token_urlsafe(32))
    path.touch(mode=0o600, exist_ok=False)
    path.write_text(text)
    print('Created .env with unique development secrets')
