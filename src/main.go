// dokploytools: manage many Dokploy panels from one CLI.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
)

// version is set at release time: -ldflags "-X main.version=v1.2.3"
var version = "dev"

func run(pos []string, f F) {
	arg := func(i int) string {
		if len(pos) > i {
			return pos[i]
		}
		return ""
	}
	// withPanel: PANEL is pos[1] and extra args follow.
	one := func(min int, fn func(Panel, []string) error) {
		each(pos[1], func(p Panel) error { return fn(p, pos[2:]) })
	}
	cred := func(url string) string {
		if f.s("key") != "" {
			return f.s("key")
		}
		if f.s("user") != "" {
			return loginKey(url, f.s("user"), need(f.s("pass"), "--pass", "Password", true))
		}
		return need("", "--key", "Panel API key", true)
	}
	switch pos[0] {
	case "panel":
		ps := loadFile()
		switch arg(1) {
		case "add":
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
			dieHint("Run `dokploytools help panel` for usage.", "Unknown command: panel %s", arg(1))
		}
	case "help":
		printHelp(pos[1:])
	case "version":
		fmt.Printf("dokploytools %s\nrepo   %s\ngo     %s\n", version, repoURL, strings.TrimPrefix(runtime.Version(), "go"))
	case "update":
		cmdSelfUpdate(f.has("check"))
	case "upgrade":
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
		each(pos[2], func(p Panel) error { return cmdSvc(p, pos[1], pos[3], f) })
	case "env":
		one(2, func(p Panel, a []string) error { return cmdEnv(p, a[0], a[1:], f) })
	case "deploy":
		one(1, func(p Panel, _ []string) error { return cmdDeploy(p, f) })
	case "backup":
		each(arg(1), func(p Panel) error { return cmdBackup(p, f) })
	case "dbbackup":
		each(pos[2], func(p Panel) error { return cmdDBBackup(p, append([]string{pos[1]}, pos[3:]...), f) })
	case "domains":
		each(arg(1), cmdDomains)
	case "domain":
		each(pos[2], func(p Panel) error { return cmdDomain(p, append([]string{pos[1]}, pos[3:]...), f) })
	case "cert":
		each(pos[2], func(p Panel) error { return cmdCert(p, append([]string{pos[1]}, pos[3:]...), f) })
	case "traefik":
		each(pos[2], func(p Panel) error { return cmdTraefik(p, append([]string{pos[1]}, pos[3:]...), f) })
	case "users":
		each(arg(1), cmdUsers)
	case "member":
		each(pos[2], func(p Panel) error { return cmdMember(p, append([]string{pos[1]}, pos[3:]...), f) })
	case "apikey":
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
		printJSON(out)
	default:
		dieHint("Run `dokploytools help` to list commands.", "Unknown command: %s", pos[0])
	}
}

func main() {
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "-v" || args[0] == "--version") {
		args = []string{"version"}
	}
	quiet = os.Getenv("DOKPLOYTOOLS_QUIET") != ""
	helpWanted := false
	var rest []string
	for _, a := range args {
		switch a {
		case "-q", "--quiet":
			quiet = true
		case "-h", "--help":
			helpWanted = true
		default:
			rest = append(rest, a)
		}
	}
	pos, f := parse(rest)
	jsonOut = f.has("json")
	if len(pos) == 0 || helpWanted {
		if helpWanted && len(pos) > 0 {
			pos = append([]string{"help"}, pos...)
		} else {
			pos = []string{"help"}
		}
	} else {
		pos = fillMissing(pos)
	}
	if pos[0] == "update" || pos[0] == "panel" && len(pos) > 1 && pos[1] == "add" || pos[0] == "deploy" {
		banner()
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
