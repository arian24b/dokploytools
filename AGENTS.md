# AGENTS.md

Go CLI (package main; stdlib + `minio-go` for S3, `x/crypto/scrypt`) managing multiple Dokploy panels. See README.md for usage.

## Layout
- `core.go` config/crypto/http/output/flags · `services.go` projects, svc, env, deploy · `backup.go` S3 + backups · `net.go` domains, certs, traefik, alert, members · `main.go` dispatch/usage.
- `Panel` + `load/save/pick`: config in `~/.config/dokploytools/panels.json`.
- `Panel.call(method, path, body, out)`: all Dokploy requests (`/api/<path>`, `x-api-key`). GET when body is nil, else POST.
- `cmd*` functions: one per subcommand; `each(name, fn)` fans out across panels.
- `parse()`/`F`: hand-rolled flags (add new boolean flags to `boolFlags`). Output goes through `table()`/`done()` so `--json` works; never `fmt.Println` results directly.

## Conventions
- Keep it single-file and dependency-light; don't add a CLI framework.
- Shortcuts get a `ponytail:` comment naming the ceiling.
- Panel responses are decoded into `map[string]any` on purpose (API shape varies by Dokploy version); use `str()`/`list()` helpers.

## Verify
- `go vet . && go test ./... && go build -o dokploytools .`
- Tests use a mock server; API paths are unverified against a live panel (see README Status). Probe with `raw`.

## Gotchas
- `go mod tidy` is slow (large minio-go tree); run it in the background.
- Never commit the built binary or real API keys.
