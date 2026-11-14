#!/bin/sh
# Nightly backup: the database (pg_dump custom format) and the blob store,
# each encrypted to BACKUP_AGE_RECIPIENT and uploaded to R2. Only the holder
# of the matching age private key can read them. Any failure emails
# ALERT_EMAIL through the server's SMTP account.
set -eu -o pipefail
stamp=$(date -u +%Y-%m-%dT%H%MZ)
step=configuration

alert() {
  [ -n "${ALERT_EMAIL:-}" ] && [ -n "${SMTP_ADDR:-}" ] || { echo "no ALERT_EMAIL or SMTP_ADDR; alert not sent" >&2; return 0; }
  printf 'From: Lykeion backup <%s>\r\nTo: %s\r\nSubject: Lykeion backup failed (%s)\r\n\r\nThe nightly backup %s failed during: %s.\r\nDetails are in ~/lykeion/backup.log on the VPS.\r\n' \
    "$SMTP_FROM" "$ALERT_EMAIL" "$step" "$stamp" "$step" |
    curl -sS --ssl-reqd --url "smtp://$SMTP_ADDR" --user "$SMTP_USER:$SMTP_PASS" \
      --mail-from "$SMTP_FROM" --mail-rcpt "$ALERT_EMAIL" -T - \
    && echo "alert sent" >&2
}
trap 'status=$?; [ "$status" -eq 0 ] || { echo "backup $stamp failed during $step" >&2; alert; }' EXIT

: "${BACKUP_AGE_RECIPIENT:?}" "${R2_ACCESS_KEY_ID:?}" "${R2_SECRET_ACCESS_KEY:?}" "${R2_ENDPOINT:?}" "${R2_BUCKET:?}"
export RCLONE_CONFIG_R2_TYPE=s3 RCLONE_CONFIG_R2_PROVIDER=Cloudflare \
  RCLONE_CONFIG_R2_ACCESS_KEY_ID="$R2_ACCESS_KEY_ID" \
  RCLONE_CONFIG_R2_SECRET_ACCESS_KEY="$R2_SECRET_ACCESS_KEY" \
  RCLONE_CONFIG_R2_ENDPOINT="$R2_ENDPOINT" RCLONE_S3_NO_CHECK_BUCKET=true

step=database
pg_dump -Fc "$MIGRATE_DATABASE_URL" | age -r "$BACKUP_AGE_RECIPIENT" \
  | rclone rcat "r2:$R2_BUCKET/db/$stamp.dump.age"
step=blobs
tar -C /data/blobs -czf - . | age -r "$BACKUP_AGE_RECIPIENT" \
  | rclone rcat "r2:$R2_BUCKET/blobs/$stamp.tar.gz.age"
echo "backup $stamp uploaded"
