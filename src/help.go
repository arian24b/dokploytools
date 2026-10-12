package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

const repoURL = "https://github.com/arian24b/dokploytools"

type opt struct{ flag, desc string }

// doc describes one command: name words, positional args, one-line description and its own options.
type doc struct {
	name, args, desc string
	opts             []opt
}

var keyOpts = []opt{{"--key KEY", "panel API key"}, {"--user EMAIL --pass PASS", "log in once and create an API key instead of --key"}}

var docs = []doc{
	{"panel add", "NAME URL", "add a panel", keyOpts},
	{"panel list", "", "list panels", nil},
	{"panel edit", "NAME", "change a panel's URL or key", append([]opt{{"--url URL", "new panel URL"}}, keyOpts...)},
	{"panel rm", "NAME", "remove a panel", nil},
	{"panel encrypt", "", "seal stored keys (needs DOKPLOYTOOLS_PASSPHRASE)", nil},
	{"projects", "[NAME]", "list projects", nil},
	{"status", "[NAME]", "overview of projects and services", []opt{{"--project P", "only this project"}}},
	{"servers", "[NAME]", "list servers", nil},
	{"containers", "[NAME]", "list containers", []opt{{"--stats", "CPU/memory per service (needs monitoring)"}}},
	{"deployments", "NAME PROJ/SVC", "recent deployments of a service", []opt{{"--limit N", "how many to show"}}},
	{"monitor", "[NAME]", "server metrics", nil},
	{"upgrade", "[NAME]", "update Dokploy itself on the panel(s)", []opt{{"--check", "report only, change nothing"}}},
	{"alert", "[NAME]", "check CPU/memory/disk/certs and send alerts", []opt{
		{"--cpu N", "default 90"}, {"--mem N", "default 90"}, {"--disk N", "default 90"}, {"--cert-days N", "default 14"},
		{"--no-certs", "skip TLS expiry checks"}, {"--webhook URL", "post alerts to a webhook"},
		{"--telegram TOKEN@CHATID", "send alerts to Telegram"}, {"--require-metrics", "fail when metrics are unavailable"}}},
	{"svc", "ACTION NAME PROJ/SVC", "ACTION = start|stop|deploy|redeploy", nil},
	{"env", "NAME PROJ/SVC [set K=V.. | unset K..]", "show, set or unset environment variables", []opt{{"--file F", "replace all variables from a file"}}},
	{"deploy", "NAME", "deploy a compose app (creates project/env/service if missing)", []opt{
		{"--repo URL", "git repository"}, {"--file F", "local compose file instead of --repo"}, {"--branch B", "default main"},
		{"--compose-path P", "default ./docker-compose.yml"}, {"--project P", "default repo name"}, {"--name N", "service name, default repo name"},
		{"--set K=V", "repeatable"}, {"--env-file F", "variables from a file"}, {"--domain H", "requires --service and --port"},
		{"--ssh-key-id ID", "ssh key for private repos"}, {"--auto-deploy", "deploy on push"}, {"--no-deploy", "create only"}}},
	{"backup", "[NAME]", "panel (web-server) backup", nil},
	{"dbbackup create", "NAME PROJ/SVC", "schedule a database backup", []opt{{"--database DB", "database name"}, {"--disabled", "create it disabled"}}},
	{"dbbackup list", "NAME [PROJ/SVC]", "list database backups", nil},
	{"dbbackup run", "NAME PROJ/SVC", "run a database backup now", []opt{{"--id BACKUPID", "which backup config"}}},
	{"dbbackup set", "NAME BACKUPID", "enable or disable a backup", []opt{{"--enable true|false", "new state"}}},
	{"dbbackup rm", "NAME BACKUPID", "remove a backup config", nil},
	{"dbbackup files", "NAME", "list backup files", nil},
	{"dbbackup download", "NAME", "download a backup file", nil},
	{"dbbackup copy", "NAME", "copy backups to a central S3 bucket", nil},
	{"domains", "[NAME]", "list domains", nil},
	{"domain add", "NAME PROJ/SVC", "add a domain", []opt{{"--host H", "domain name"}, {"--path P", "path prefix"},
		{"--cert letsencrypt|none|custom", "certificate type"}, {"--resolver NAME", "certificate resolver"}}},
	{"domain rm", "NAME HOST", "remove a domain", nil},
	{"domain check", "NAME [HOST]", "check DNS and certificate of domains", nil},
	{"cert list", "NAME", "list certificates", nil},
	{"cert add", "NAME CERTNAME", "upload a certificate", []opt{{"--cert FILE", "fullchain.pem"}, {"--key FILE", "privkey.pem"},
		{"--domain H", "verify the cert covers it"}, {"--auto-renew", "renew automatically"}}},
	{"cert rm", "NAME ID|CERTNAME", "remove a certificate", nil},
	{"traefik get", "NAME main|web|middleware", "print a Traefik config", []opt{{"--path FILE", "read a file instead"}}},
	{"traefik set", "NAME main|web|middleware", "replace a Traefik config", []opt{{"--from FILE", "new config (old one saved to a .bak file)"}, {"--path FILE", "write a file instead"}}},
	{"traefik ls", "NAME", "list Traefik config files", nil},
	{"traefik reload", "NAME", "reload Traefik", nil},
	{"users", "[NAME]", "list users", nil},
	{"member invite", "NAME EMAIL", "invite a member", []opt{{"--role member|admin", "role"}}},
	{"member rm", "NAME USERID", "remove a member", nil},
	{"member invitations", "NAME", "list invitations", nil},
	{"member uninvite", "NAME INVITATIONID", "cancel an invitation", nil},
	{"apikey list", "NAME", "list API keys", nil},
	{"apikey create", "NAME KEYNAME", "create an API key", []opt{{"--days N", "expiry"}}},
	{"apikey rm", "NAME KEYID", "remove an API key", nil},
	{"orgs", "[NAME]", "list organizations", nil},
	{"raw", "NAME METHOD PATH [JSON]", "call any Dokploy API route, e.g. raw prod GET project.all", nil},
	{"help", "[COMMAND..]", "show help for a command", nil},
	{"version", "", "show version and repository", nil},
	{"update", "", "update dokploytools itself (--check: only compare versions)", []opt{{"--check", "only compare versions"}}},
}

var commonOpts = []opt{
	{"--json", "machine-readable output (exit 1 if any panel failed)"},
	{"--watch SECS", "repeat the command every SECS seconds"},
	{"--env E", "environment of PROJ/SVC (default production)"},
	{"--server ID", "remote server id"},
	{"--dest D", "backup destination id or name"},
	{"--search PREFIX", "backup file prefix"},
	{"--download DIR", "download the backup file to DIR"},
	{"--to-endpoint E, --to-bucket B, --to-access K, --to-secret S, --to-prefix P, --to-region R", "copy backups to one central S3 bucket (as <prefix>/<panel>/<file>)"},
	{"--keep N", "keep the latest N backups"},
	{"--schedule CRON", "backup schedule"},
	{"--prefix P", "backup prefix"},
	{"--wait", "wait for the deployment to finish"},
	{"--timeout SECS", "wait limit"},
	{"--check", "report only, change nothing"},
	{"--org ID", "organization id"},
	{"--service S", "compose service name"},
	{"--port N", "container port"},
	{"--no-https", "serve over http only"},
}

var globalFlags = []opt{
	{"-q, --quiet", "Suppress non-essential output [boolean] [default: false]"},
	{"-h, --help", "Show help [boolean]"},
	{"-v, --version", "Show version [boolean]"},
}

func cols(rows []opt, style func(string) string) string {
	w := 0
	for _, r := range rows {
		if n := len([]rune(r.flag)); n > w && n <= 60 {
			w = n
		}
	}
	var b strings.Builder
	for _, r := range rows {
		pad := w - len([]rune(r.flag))
		if pad < 0 {
			pad = 0
		}
		fmt.Fprintf(&b, "  %s%s  %s\n", style(r.flag), strings.Repeat(" ", pad), r.desc)
	}
	return b.String()
}

func heading(s string) string { return paint("brand", bold(s, os.Stdout), os.Stdout) }
func code(s string) string    { return paint("info", s, os.Stdout) }

func printHelp(path []string) {
	banner()
	var match []doc
	for _, d := range docs {
		if hasPrefix(strings.Fields(d.name), path) {
			match = append(match, d)
		}
	}
	if len(path) > 0 && len(match) == 0 {
		dieHint("Run `dokploytools help` to list commands.", "Unknown command: %s", strings.Join(path, " "))
	}
	var b strings.Builder
	if len(path) == 0 {
		b.WriteString("dokploytools <command> [options]\n\nManage multiple Dokploy panels from one CLI.\n")
		b.WriteString("NAME omitted or \"all\" = every panel. PROJ/SVC = project name / service name.\n\n")
		b.WriteString(heading("Commands") + "\n")
		rows := make([]opt, 0, len(docs))
		for _, d := range docs {
			rows = append(rows, opt{strings.TrimSpace("dokploytools " + d.name + " " + d.args), d.desc})
		}
		b.WriteString(cols(rows, code) + "\n")
		b.WriteString(heading("Common options") + "  (accepted by every command that has a matching purpose)\n")
		b.WriteString(cols(commonOpts, code) + "\n")
	} else {
		for i, d := range match {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(strings.TrimSpace("dokploytools "+d.name+" "+d.args) + " [options]\n\n" + d.desc + "\n")
			if len(d.opts) > 0 {
				b.WriteString("\n" + heading("Options") + "\n" + cols(d.opts, code))
			}
		}
		b.WriteString("\n" + heading("Common options") + "  (see `dokploytools help`)\n\n")
	}
	b.WriteString(heading("Global flags") + "\n" + cols(globalFlags, code) + "\n")
	b.WriteString(heading("Environment") + "\n")
	b.WriteString("  DOKPLOY_<NAME>_URL + DOKPLOY_<NAME>_KEY define a panel; DOKPLOYTOOLS_PASSPHRASE encrypts stored keys.\n")
	b.WriteString("  Config: ~/.config/dokploytools/panels.json\n\n")
	b.WriteString(paint("muted", "For more information, visit "+repoURL, os.Stdout) + "\n")
	fmt.Print(b.String())
}

func hasPrefix(words, prefix []string) bool {
	if len(prefix) > len(words) {
		return false
	}
	for i, p := range prefix {
		if words[i] != p {
			return false
		}
	}
	return true
}

var optionalRe = regexp.MustCompile(`\[[^\]]*\]`)

// fillMissing asks (TTY) or fails (pipe/CI) for required positional arguments the command was not given.
func fillMissing(pos []string) []string {
	var best *doc
	for i := range docs {
		w := strings.Fields(docs[i].name)
		if len(pos) >= len(w) && hasPrefix(pos, w) && (best == nil || len(w) > len(strings.Fields(best.name))) {
			best = &docs[i]
		}
	}
	if best == nil {
		return pos
	}
	req := strings.Fields(optionalRe.ReplaceAllString(best.args, ""))
	have := len(pos) - len(strings.Fields(best.name))
	labels := map[string]string{"NAME": "Panel name", "PROJ/SVC": "Project/service (PROJ/SVC)"}
	for i := have; i < len(req); i++ {
		if !interactive() {
			dieHint("Usage: dokploytools "+best.name+" "+best.args, "<%s> is required. Pass it as an argument or run interactively.", req[i])
		}
		label := labels[req[i]]
		if label == "" {
			label = req[i]
		}
		uiWarn("<%s> is required", req[i])
		pos = append(pos, ask(label, false))
	}
	return pos
}
