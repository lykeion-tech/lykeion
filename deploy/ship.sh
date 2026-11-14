#!/bin/sh
# Ship a commit to the VPS and rebuild the stack there.
# Usage: LYK_DEPLOY_HOST=deploy@<ip> deploy/ship.sh [ref]
# The VPS keeps its secrets in ~/lykeion/.env; the source is replaced each time.
set -eu
host=${LYK_DEPLOY_HOST:?set LYK_DEPLOY_HOST=deploy@<ip>}
ref=${1:-HEAD}
git archive --format=tar "$ref" | ssh "$host" '
  set -eu
  test -f ~/lykeion/.env || { echo "missing ~/lykeion/.env" >&2; exit 1; }
  rm -rf ~/lykeion/src.new && mkdir ~/lykeion/src.new && tar -x -C ~/lykeion/src.new
  rm -rf ~/lykeion/src && mv ~/lykeion/src.new ~/lykeion/src
  cd ~/lykeion/src/deploy
  docker compose --env-file ~/lykeion/.env up -d --build --remove-orphans
  docker compose --env-file ~/lykeion/.env --profile backup build -q backup
  docker image prune -f >/dev/null
  docker compose --env-file ~/lykeion/.env ps
'
