#!/usr/bin/env python3
"""Run sequential backend tests against the isolated development PostgreSQL only."""
from pathlib import Path
import os,subprocess
from urllib.parse import quote
root=Path(__file__).resolve().parents[1]
password=(root/'.local-state/development.env').read_text().strip().split('=',1)[1]
env={**os.environ,'GOCACHE':'/tmp/lykeion-redesign-gocache','LYKEION_TEST_DATABASE_URL':f'postgres://lykeion_dev:{quote(password,safe="")}@127.0.0.1:15439/lykeion_development?sslmode=disable'}
raise SystemExit(subprocess.call(['go','test','-race','-count=1','./...'],cwd=root/'backend',env=env))
