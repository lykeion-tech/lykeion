# Lykeion Desktop server configuration

Standard desktop builds connect to the hosted Lykeion service at
`https://app.lykeion.tech` by default. A release may specify a different hosted
address. To connect to a self-hosted server, create a UTF-8 JSON file at:

- macOS and Linux: `~/.lyk/desktop.json`
- Windows: `%USERPROFILE%\.lyk\desktop.json`

For example:

```json
{
  "schemaVersion": 1,
  "apiUrl": "https://your-server.example"
}
```

Use the server origin: scheme, hostname and optional port. Remote servers must
use HTTPS. HTTP is accepted only for `localhost`, `127.0.0.1` or `[::1]` on
the same computer, such as `http://localhost:8080`. Do not include credentials,
a path, query string or fragment. Quit and reopen Lykeion after changing the
file. Removing the file restores the hosted default; an invalid or unreadable
file stops startup instead of connecting to a different server.
