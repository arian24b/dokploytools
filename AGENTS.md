# AGENTS.md

Go CLI (`main.go`, one file, stdlib + `minio-go` for S3 downloads) managing multiple Dokploy panels. See README.md for usage.

## Layout
- `Panel` + `load/save/pick`: config in `~/.config/dokploytools/panels.json`.
- `Panel.call(method, path, body, out)`: all Dokploy requests (`/api/<path>`, `x-api-key`). GET when body is nil, else POST.
- `cmd*` functions: one per subcommand; `each(name, fn)` fans out across panels.
- `main()`: hand-rolled arg parsing (`flags`), no CLI framework.

## Conventions
- Keep it single-file and dependency-light; don't add a CLI framework.
- Shortcuts get a `ponytail:` comment naming the ceiling.
- Panel responses are decoded into `map[string]any` on purpose (API shape varies by Dokploy version); use `str()`/`list()` helpers.

## Verify
- `go vet . && go build -o dokploytools .`
- No automated tests; API paths are unverified against a live panel (see README Status). Probe with `raw`.

## Gotchas
- `go mod tidy` is slow (large minio-go tree); run it in the background.
- Never commit the built binary or real API keys.
