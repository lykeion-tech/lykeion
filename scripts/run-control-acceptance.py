#!/usr/bin/env python3
"""Explicit disposable S01 UI workspace. Never reads the installed app's data."""
import os
import secrets
import subprocess
from pathlib import Path
from urllib.parse import quote
root = Path(__file__).resolve().parents[1]
state = root / '.local-state'
state.mkdir(exist_ok=True)
password = (state / 'development.env').read_text().strip().split('=', 1)[1]
schema = 'ui_' + secrets.token_hex(8)
subprocess.run(['docker', 'compose', '--env-file', str(state/'development.env'), '-f', str(root/'deploy/compose.dev.yaml'), 'exec', '-T', 'postgres', 'psql', '-U', 'lykeion_dev', '-d', 'lykeion_development', '-v', 'ON_ERROR_STOP=1', '-c', 'CREATE SCHEMA '+schema],check=True,stdout=subprocess.DEVNULL)
key = state / (schema+'.key')
with key.open('xb') as f:
    os.chmod(key,0o600)
    f.write(secrets.token_bytes(32))
env = {**os.environ, 'GOCACHE':'/tmp/lykeion-redesign-gocache', 'LYKEION_DATABASE_URL':f'postgres://lykeion_dev:{quote(password,safe="")}@127.0.0.1:15439/lykeion_development?sslmode=disable&search_path={schema}', 'LYKEION_SIGNING_KEY_FILE':str(key), 'LYKEION_ALLOW_LOOPBACK_HTTP':'1','LYKEION_ALLOWED_ORIGINS':'http://127.0.0.1:1430,http://localhost:1430,lykeion://desktop','LYKEION_LISTEN':'127.0.0.1:18431'}
code = state / (schema+'.code')
subprocess.run(['go','run','./cmd/lykeion','init','--owner','S01 Test Scientist','--workspace','S01 acceptance · disposable','--code-file',str(code)],env=env,cwd=root/'backend',check=True)
(state/'acceptance-code-path').write_text(str(code))
print('Disposable schema:',schema,flush=True)
raise SystemExit(subprocess.call(['go','run','./cmd/control'],env=env,cwd=root/'backend'))
