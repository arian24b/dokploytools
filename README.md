# dokploytools

[فارسی](README.fa.md)

Go CLI to manage multiple [Dokploy](https://dokploy.com) panels. `NAME` = panel name; omit it (or use `all`) to run on every panel.

```
go build -o dokploytools ./src
ln -sf "$PWD/dokploytools" ~/.local/bin/dpt   # short alias: dpt (or: alias dpt=dokploytools)
```

Global flags: `--json` (machine-readable rows, exit 1 if any panel errored) and `--watch SECS` (repeat).

## Panels & config
```
panel add NAME URL (--key KEY | --user EMAIL --pass PASS)
panel list | panel edit NAME [--url U] [--key K] | panel rm NAME | panel encrypt
```
- Stored in `~/.config/dokploytools/panels.json` (0600). Set `DOKPLOYTOOLS_PASSPHRASE` and keys are AES-GCM encrypted (scrypt) on save; `panel encrypt` seals an existing file.
- No file needed: `DOKPLOY_<NAME>_URL` + `DOKPLOY_<NAME>_KEY` define panel `<name>` (never written to disk).

## Overview
```
projects | status [--project P] | servers [--check] | containers [--stats]
deployments NAME PROJ/SVC | monitor | update [--check]
alert [--cpu 90 --mem 90 --disk 90 --cert-days 14 --no-certs --webhook URL --telegram TOKEN@CHATID]
```
`alert` exits 1 when the panel is down, a service/server is in error, a metric is over its limit or an HTTPS domain's cert is near expiry — put it in cron.

## Services
```
svc start|stop|deploy|redeploy NAME PROJ/SVC [--env E] [--wait]     # NAME=all hits every panel
env NAME PROJ/SVC [set K=V.. | unset K.. | --file F]
```
**One-line compose deploy from a git repo** (creates project/environment/compose if missing, updates if present):
```
dokploytools deploy prod --repo https://github.com/me/app.git --branch main \
  --domain app.example.com --service web --port 3000 --set KEY=val --wait
```
Other flags: `--project P` (default = repo name), `--env production`, `--name`, `--compose-path`, `--env-file F`, `--ssh-key-id`, `--server ID`, `--auto-deploy`, `--no-deploy`, `--no-https`; `--file compose.yml` deploys a local compose file instead of a repo.

## Backups
```
backup [NAME] [--dest D] [--keep N] [--download DIR]                        # panel (web-server) backup
dbbackup create NAME PROJ/SVC [--dest D --schedule "0 2 * * *" --keep 7 --database DB --prefix P --disabled]
dbbackup list NAME [PROJ/SVC] | run NAME PROJ/SVC [--id B] | set NAME BACKUPID [--schedule --keep --enable false] | rm NAME BACKUPID
dbbackup files NAME --dest D --search PREFIX | download NAME --dest D --search PREFIX --download DIR
dbbackup copy all --dest D --search PREFIX --to-endpoint E --to-bucket B --to-access K --to-secret S [--to-prefix P]
```
`backup`/`dbbackup run` also accept `--download DIR` and the `--to-*` flags (or `DOKPLOYTOOLS_TO_ENDPOINT/BUCKET/ACCESS/SECRET`) to mirror the new file into one central bucket under `<to-prefix>/<panel>/`. Postgres, MySQL, MariaDB, Mongo supported. **Restore is not exposed by Dokploy's REST API** (it uses a websocket subscription), so it is not implemented.

## Domains, certificates, Traefik
```
domains | domain add NAME PROJ/SVC --host H --port N [--service S] [--cert letsencrypt|none|custom] [--no-https]
domain rm NAME HOST | domain check NAME [HOST]        # panel DNS check + live TLS expiry
cert list NAME | cert add NAME CERTNAME --cert fullchain.pem --key privkey.pem [--domain H] [--auto-renew] | cert rm NAME ID|NAME
traefik get NAME main|web|middleware | traefik get NAME --path FILE | traefik ls NAME
traefik set NAME main|web|middleware --from FILE | traefik reload NAME
```
For a domain whose DNS is **not** on the server (CDN/other host, so Let's Encrypt HTTP challenge fails): upload its cert with `cert add` (validated locally: key pair, expiry, `--domain` coverage), then add the domain with `--cert none`; Traefik serves the uploaded cert by SNI. `traefik set` saves the previous config to `traefik-<panel>-<file>-<ts>.bak` first.

## People
```
users | member invite NAME EMAIL [--role admin] | member rm NAME USERID | member invitations NAME | member uninvite NAME ID
apikey list|create NAME [--days N]|rm NAME ID | orgs
```

## Status
Endpoint paths and bodies follow the Dokploy docs reference. Untested against a live panel; **least certain**: `project.all` nesting (handled for environments and legacy layout), `user.getMetricsToken` / monitoring port + `/metrics`, `user.getContainerMetrics`, `auth/organization/invite-member` (better-auth), login → API key. Use `raw NAME GET PATH` to probe. `go test ./...` runs a mock-server deploy test.

## CI & releases
- `.github/workflows/ci.yml`: gofmt check, `go vet`, `go test -race` on every push to `main` and every PR.
- `.github/workflows/release.yml`: push a tag (`git tag v1.2.3 && git push --tags`) to run tests, build linux/darwin (amd64, arm64) and windows binaries with the tag embedded (`dokploytools version`), and publish a GitHub release with checksums and generated notes.

### PyPI
Each release also publishes platform wheels (the Go binary + `dokploytools`/`dpt` entry points) to PyPI: `pipx install dokploytools` (or `uv tool install dokploytools`), then run `dpt`. Needs the repo secret `PYPI_API_TOKEN`; tags must be PEP 440 compatible (`v1.2.3`, not `v1.2.3-beta`). Wheels are built by `scripts/build_wheels.py` (stdlib only).
