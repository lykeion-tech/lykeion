# Build context: the repository root.

FROM golang:1.26-alpine AS go
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/server ./cmd/migrate

FROM node:24-alpine AS dashboard
WORKDIR /src
COPY package.json package-lock.json ./
RUN ELECTRON_SKIP_BINARY_DOWNLOAD=1 npm ci --no-audit --no-fund
COPY . .
ARG PUBLIC_URL
RUN test -n "$PUBLIC_URL" \
 && npx vite build --config apps/web/vite.config.ts \
 && LYK_API_URL="$PUBLIC_URL" npx vite build --config apps/dashboard/vite.config.ts --base /app/

FROM alpine:3.22 AS server
RUN apk add --no-cache ca-certificates postgresql17-client \
 && adduser -D -H -u 10001 lykeion \
 && install -d -o lykeion -g lykeion /data/blobs
COPY --from=go /out/server /out/migrate /usr/local/bin/
USER lykeion
EXPOSE 8080
CMD ["server"]

FROM alpine:3.22 AS backup
RUN apk add --no-cache ca-certificates postgresql17-client age rclone tar
COPY deploy/backup.sh /usr/local/bin/backup
CMD ["backup"]

FROM caddy:2-alpine AS web
COPY deploy/Caddyfile /etc/caddy/Caddyfile
COPY --from=dashboard /src/dist/web /srv
COPY --from=dashboard /src/dist/dashboard /srv/app
