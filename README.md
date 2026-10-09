# dokploytools

Single-binary Go CLI to manage multiple [Dokploy](https://dokploy.com) panels.

## Build

```
go build -o dokploytools .
```

## Usage

NAME omitted = run on all panels.

```
panel add NAME URL (--key KEY | --user EMAIL --pass PASS)
panel list | panel edit NAME [--url U] [--key K | --user E --pass P] | panel rm NAME
update   [NAME] [--check]              update Dokploy when an update is available
projects [NAME]                        projects, active/inactive
domains  [NAME]                        domains per project/service
users    [NAME]                        panel users
backup   [NAME] [--dest ID|NAME] [--download DIR]
monitor  [NAME]                        health + CPU/mem/disk
raw NAME METHOD PATH [JSON]            raw API call, e.g. raw prod GET project.all
```

- Panels live in `~/.config/dokploytools/panels.json` (mode 0600, API keys in plain text).
- `--user/--pass` signs in once, mints an API key, and stores only the key.
- `backup` triggers a web-server backup to the panel's S3 destination, then (with `--download`) fetches the newest file using the destination's credentials.
- `monitor` needs the panel's monitoring service enabled and its port reachable.

## Status

Endpoint paths were written from memory of Dokploy's tRPC/OpenAPI API and are **not verified** against a live panel:
`user.all`, `settings.updateServer`, `settings.getUpdateData`, `user.getBackups`, `user.getMetricsToken`, `auth/sign-in/email`, `auth/api-key/create`.
Check your panel's Swagger UI (`<host>:3000/swagger`) and use `raw` to probe.
