#!/bin/sh
# Nightly backup: the database (pg_dump custom format) and the blob store,
# each encrypted to BACKUP_AGE_RECIPIENT and uploaded to R2. Only the holder
# of the matching age private key can read them.
set -eu -o pipefail
: "${BACKUP_AGE_RECIPIENT:?}" "${R2_ACCESS_KEY_ID:?}" "${R2_SECRET_ACCESS_KEY:?}" "${R2_ENDPOINT:?}" "${R2_BUCKET:?}"
stamp=$(date -u +%Y-%m-%dT%H%MZ)
export RCLONE_CONFIG_R2_TYPE=s3 RCLONE_CONFIG_R2_PROVIDER=Cloudflare \
  RCLONE_CONFIG_R2_ACCESS_KEY_ID="$R2_ACCESS_KEY_ID" \
  RCLONE_CONFIG_R2_SECRET_ACCESS_KEY="$R2_SECRET_ACCESS_KEY" \
  RCLONE_CONFIG_R2_ENDPOINT="$R2_ENDPOINT" RCLONE_S3_NO_CHECK_BUCKET=true

pg_dump -Fc "$MIGRATE_DATABASE_URL" | age -r "$BACKUP_AGE_RECIPIENT" \
  | rclone rcat "r2:$R2_BUCKET/db/$stamp.dump.age"
tar -C /data/blobs -czf - . | age -r "$BACKUP_AGE_RECIPIENT" \
  | rclone rcat "r2:$R2_BUCKET/blobs/$stamp.tar.gz.age"
echo "backup $stamp uploaded"
