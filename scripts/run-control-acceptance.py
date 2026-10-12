#!/usr/bin/env python3
"""Explicit disposable S01 UI workspace. Never reads the installed app's data."""
import argparse
import os
import re
import secrets
import subprocess
from pathlib import Path
from urllib.parse import quote
root = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument('--resume', help='Existing disposable ui_<hex> schema; no accounts are created')
args = parser.parse_args()
state = root / '.local-state'
state.mkdir(exist_ok=True)
password = (state / 'development.env').read_text().strip().split('=', 1)[1]
schema = args.resume or 'ui_' + secrets.token_hex(8)
if not re.fullmatch(r'ui_[a-f0-9]{16}', schema):
    raise SystemExit('Only a disposable acceptance schema may be resumed')
key = state / (schema+'.key')
if args.resume:
    if not key.is_file():
        raise SystemExit('Private acceptance signing key not found')
else:
    subprocess.run(['docker', 'compose', '--env-file', str(state/'development.env'), '-f', str(root/'deploy/compose.dev.yaml'), 'exec', '-T', 'postgres', 'psql', '-U', 'lykeion_dev', '-d', 'lykeion_development', '-v', 'ON_ERROR_STOP=1', '-c', 'CREATE SCHEMA '+schema],check=True,stdout=subprocess.DEVNULL)
    with key.open('xb') as f:
        os.chmod(key,0o600)
        f.write(secrets.token_bytes(32))
env = {**os.environ, 'GOCACHE':'/tmp/lykeion-redesign-gocache', 'LYKEION_DATABASE_URL':f'postgres://lykeion_dev:{quote(password,safe="")}@127.0.0.1:15439/lykeion_development?sslmode=disable&search_path={schema}', 'LYKEION_SIGNING_KEY_FILE':str(key), 'LYKEION_ARTIFACT_ROOT':str(state/(schema+'-artifacts')), 'LYKEION_ALLOW_LOOPBACK_HTTP':'1','LYKEION_ALLOWED_ORIGINS':'http://127.0.0.1:1430,http://localhost:1430,lykeion://desktop','LYKEION_LISTEN':'127.0.0.1:18431'}
if not args.resume:
    code = state / (schema+'.code')
    subprocess.run(['go','run','./cmd/lykeion','init','--owner','S01 Test Scientist','--workspace','S01 acceptance · disposable','--code-file',str(code)],env=env,cwd=root/'backend',check=True)
    (state/'acceptance-code-path').write_text(str(code))
print('Disposable schema:',schema,flush=True)
raise SystemExit(subprocess.call(['go','run','./cmd/control'],env=env,cwd=root/'backend'))
