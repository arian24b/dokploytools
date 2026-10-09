// dokploytools: manage many Dokploy panels from one CLI.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// version is set at release time: -ldflags "-X main.version=v1.2.3"
var version = "dev"

const usage = `dokploytools — manage multiple Dokploy panels

NAME omitted or "all" = every panel. PROJ/SVC = project name / service name.

Common options (accepted by every command that has a matching purpose):
	--json			machine-readable output (exit 1 if any panel failed)
	--watch SECS		repeat the command every SECS seconds
	--env E			environment of PROJ/SVC (default production)
	--server ID		remote server id
	--dest D		backup destination id or name
	--search PREFIX		backup file prefix
	--download DIR		download the backup file to DIR
	--to-endpoint E, --to-bucket B, --to-access K, --to-secret S, --to-prefix P, --to-region R
			copy backups to one central S3 bucket (as <prefix>/<panel>/<file>)
	--keep N		keep the latest N backups
	--schedule CRON		backup schedule
	--prefix P		backup prefix
	--wait			wait for the deployment to finish
	--timeout SECS		wait limit
	--check			report only, change nothing
	--org ID		organization id
	--service S		compose service name
	--port N		container port
	--no-https		serve over http only
	--key KEY		panel API key
	--user EMAIL --pass PASS
			log in once and create an API key instead of --key

Commands:
panel add NAME URL
panel list
panel edit NAME
	--url URL
panel rm NAME
panel encrypt	seal stored keys (needs DOKPLOYTOOLS_PASSPHRASE)
projects [NAME]
status [NAME]
	--project P	only this project
servers [NAME]
containers [NAME]
	--stats	CPU/memory per service (needs monitoring)
deployments NAME PROJ/SVC
	--limit N
monitor [NAME]
update [NAME]
alert [NAME]
	--cpu N		default 90
	--mem N		default 90
	--disk N		default 90
	--cert-days N	default 14
	--no-certs	skip TLS expiry checks
	--webhook URL
	--telegram TOKEN@CHATID
	--require-metrics
svc ACTION NAME PROJ/SVC	ACTION = start|stop|deploy|redeploy
env NAME PROJ/SVC [set K=V.. | unset K..]
	--file F	replace all variables from a file
deploy NAME	deploy a compose app (creates project/env/service if missing)
	--repo URL		git repository
	--file F		local compose file instead of --repo
	--branch B		default main
	--compose-path P	default ./docker-compose.yml
	--project P		default repo name
	--name N		service name, default repo name
	--set K=V		repeatable
	--env-file F
	--domain H		requires --service and --port
	--ssh-key-id ID
	--auto-deploy
	--no-deploy
backup [NAME]	panel (web-server) backup
dbbackup create NAME PROJ/SVC
	--database DB
	--disabled
dbbackup list NAME [PROJ/SVC]
dbbackup run NAME PROJ/SVC
	--id BACKUPID
dbbackup set NAME BACKUPID
	--enable true|false
dbbackup rm NAME BACKUPID
dbbackup files NAME
dbbackup download NAME
dbbackup copy NAME
domains [NAME]
domain add NAME PROJ/SVC
	--host H
	--path P
	--cert letsencrypt|none|custom
	--resolver NAME
domain rm NAME HOST
domain check NAME [HOST]
cert list NAME
cert add NAME CERTNAME
	--cert FILE	fullchain.pem
	--key FILE	privkey.pem
	--domain H	verify the cert covers it
	--auto-renew
cert rm NAME ID|CERTNAME
traefik get NAME main|web|middleware
	--path FILE	read a file instead
traefik set NAME main|web|middleware
	--from FILE	new config (old one saved to a .bak file)
	--path FILE	write a file instead
traefik ls NAME
traefik reload NAME
users [NAME]
member invite NAME EMAIL
	--role member|admin
member rm NAME USERID
member invitations NAME
member uninvite NAME INVITATIONID
apikey list NAME
apikey create NAME KEYNAME
	--days N	expiry
apikey rm NAME KEYID
orgs [NAME]
raw NAME METHOD PATH [JSON]	e.g. raw prod GET project.all
version

Environment: DOKPLOY_<NAME>_URL + DOKPLOY_<NAME>_KEY define a panel; DOKPLOYTOOLS_PASSPHRASE encrypts stored keys.
Config: ~/.config/dokploytools/panels.json`

func run(pos []string, f F) {
	arg := func(i int) string {
		if len(pos) > i {
			return pos[i]
		}
		return ""
	}
	// withPanel: PANEL is pos[1] and extra args follow.
	one := func(min int, fn func(Panel, []string) error) {
		if len(pos) < min+1 {
			die("missing arguments; see usage")
		}
		each(pos[1], func(p Panel) error { return fn(p, pos[2:]) })
	}
	cred := func(url string) string {
		if f.s("key") != "" {
			return f.s("key")
		}
		if f.s("user") != "" {
			return loginKey(url, f.s("user"), f.s("pass"))
		}
		die("need --key or --user/--pass")
		return ""
	}
	switch pos[0] {
	case "panel":
		ps := loadFile()
		switch arg(1) {
		case "add":
			if arg(3) == "" {
				die("panel add NAME URL ...")
			}
			for _, p := range ps {
				if p.Name == arg(2) {
					die("panel %q exists", arg(2))
				}
			}
			save(append(ps, Panel{arg(2), arg(3), cred(arg(3))}))
		case "list":
			rows := [][]string{{"NAME", "URL", "KEY"}}
			for _, p := range load() {
				rows = append(rows, []string{p.Name, p.URL, "***" + p.APIKey[max(0, len(p.APIKey)-4):]})
			}
			table(rows)
		case "edit":
			for i := range ps {
				if ps[i].Name == arg(2) {
					if f.s("url") != "" {
						ps[i].URL = f.s("url")
					}
					if f.s("key") != "" || f.s("user") != "" {
						ps[i].APIKey = cred(ps[i].URL)
					}
					save(ps)
					return
				}
			}
			die("unknown panel %q", arg(2))
		case "rm":
			var out []Panel
			for _, p := range ps {
				if p.Name != arg(2) {
					out = append(out, p)
				}
			}
			if len(out) == len(ps) {
				die("unknown panel %q", arg(2))
			}
			save(out)
		case "encrypt":
			save(ps) // save() seals when DOKPLOYTOOLS_PASSPHRASE is set
		default:
			die("panel add|list|edit|rm|encrypt")
		}
	case "version", "--version":
		fmt.Println("dokploytools", version)
	case "update":
		each(arg(1), func(p Panel) error { return cmdUpdate(p, f.has("check")) })
	case "projects":
		each(arg(1), cmdProjects)
	case "status":
		each(arg(1), func(p Panel) error { return cmdStatus(p, f) })
	case "servers":
		each(arg(1), func(p Panel) error { return cmdServers(p, f) })
	case "containers":
		each(arg(1), func(p Panel) error { return cmdContainers(p, f) })
	case "deployments":
		one(2, func(p Panel, a []string) error { return cmdDeployments(p, a[0], f) })
	case "monitor":
		each(arg(1), cmdMonitor)
	case "alert":
		each(arg(1), func(p Panel) error { return cmdAlert(p, f) })
	case "svc":
		if len(pos) < 4 {
			die("svc start|stop|deploy|redeploy NAME PROJ/SVC")
		}
		each(pos[2], func(p Panel) error { return cmdSvc(p, pos[1], pos[3], f) })
	case "env":
		one(2, func(p Panel, a []string) error { return cmdEnv(p, a[0], a[1:], f) })
	case "deploy":
		one(1, func(p Panel, _ []string) error { return cmdDeploy(p, f) })
	case "backup":
		each(arg(1), func(p Panel) error { return cmdBackup(p, f) })
	case "dbbackup":
		if len(pos) < 3 {
			die("dbbackup ACTION NAME ...")
		}
		each(pos[2], func(p Panel) error { return cmdDBBackup(p, append([]string{pos[1]}, pos[3:]...), f) })
	case "domains":
		each(arg(1), cmdDomains)
	case "domain":
		if len(pos) < 3 {
			die("domain add|rm|check NAME ...")
		}
		each(pos[2], func(p Panel) error { return cmdDomain(p, append([]string{pos[1]}, pos[3:]...), f) })
	case "cert":
		if len(pos) < 3 {
			die("cert list|add|rm NAME ...")
		}
		each(pos[2], func(p Panel) error { return cmdCert(p, append([]string{pos[1]}, pos[3:]...), f) })
	case "traefik":
		if len(pos) < 3 {
			die("traefik get|set|ls|reload NAME ...")
		}
		each(pos[2], func(p Panel) error { return cmdTraefik(p, append([]string{pos[1]}, pos[3:]...), f) })
	case "users":
		each(arg(1), cmdUsers)
	case "member":
		if len(pos) < 3 {
			die("member invite|rm|invitations|uninvite NAME ...")
		}
		each(pos[2], func(p Panel) error { return cmdMember(p, append([]string{pos[1]}, pos[3:]...), f) })
	case "apikey":
		if len(pos) < 3 {
			die("apikey list|create|rm NAME ...")
		}
		each(pos[2], func(p Panel) error { return cmdAPIKey(p, append([]string{pos[1]}, pos[3:]...), f) })
	case "orgs":
		each(arg(1), cmdOrgs)
	case "raw":
		p := pick(arg(1))[0]
		var body any
		if arg(4) != "" {
			var o obj
			if err := json.Unmarshal([]byte(arg(4)), &o); err != nil {
				die("bad JSON: %v", err)
			}
			body = o
		}
		var out any
		if err := p.call(strings.ToUpper(arg(2)), arg(3), body, &out); err != nil {
			die("%v", err)
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
	default:
		fmt.Println(usage)
	}
}

func main() {
	pos, f := parse(os.Args[1:])
	jsonOut = f.has("json")
	if len(pos) == 0 {
		fmt.Println(usage)
		return
	}
	if f.has("watch") {
		for {
			fmt.Print("\033[H\033[2J")
			run(pos, f)
			flush()
			failed = false
			time.Sleep(time.Duration(f.n("watch", 5)) * time.Second)
		}
	}
	run(pos, f)
	flush()
	if failed {
		os.Exit(1)
	}
}
