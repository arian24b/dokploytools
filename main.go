// dokploytools: manage many Dokploy panels from one CLI.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const usage = `dokploytools — manage multiple Dokploy panels

Global: --json (machine output)  --watch SECS (repeat)   NAME omitted / "all" = every panel

panels    panel add NAME URL (--key K | --user E --pass P) | list | edit NAME [--url --key] | rm NAME | encrypt
          (env panels: DOKPLOY_<NAME>_URL + DOKPLOY_<NAME>_KEY; DOKPLOYTOOLS_PASSPHRASE encrypts keys at rest)
overview  projects [NAME] | status [NAME] [--project P] | servers [NAME] [--check] | containers [NAME] [--stats]
          deployments NAME PROJ/SVC | monitor [NAME] | update [NAME] [--check]
          alert [NAME] [--cpu 90 --mem 90 --disk 90 --cert-days 14 --no-certs --webhook URL --telegram TOKEN@CHAT]
services  svc start|stop|deploy|redeploy NAME PROJ/SVC [--env E --wait]
          env NAME PROJ/SVC [set K=V.. | unset K.. | --file F]
          deploy NAME --repo URL [--branch main --compose-path ./docker-compose.yml --project P --env production
                --name N --set K=V --env-file F --domain H --service S --port N --server ID --ssh-key-id ID
                --auto-deploy --no-deploy --wait]   (or --file compose.yml for an inline compose)
backups   backup [NAME] [--dest D --keep N --download DIR --to-endpoint --to-bucket --to-access --to-secret --to-prefix]
          dbbackup list|create|run|set|rm|files|download|copy NAME ...   (see README)
domains   domains [NAME] | domain add NAME PROJ/SVC --host H --port N [--service S --cert letsencrypt|none|custom --no-https]
          domain rm NAME HOST | domain check NAME [HOST]
certs     cert list NAME | cert add NAME CERTNAME --cert F --key F [--domain H --auto-renew] | cert rm NAME ID|NAME
traefik   traefik get NAME main|web|middleware | get NAME --path F | set NAME main|... --from FILE | ls NAME | reload NAME
people    users [NAME] | member invite|rm|invitations|uninvite NAME ... | apikey list|create|rm NAME ... | orgs [NAME]
other     raw NAME METHOD PATH [JSON]

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
