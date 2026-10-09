// dokploytools: manage many Dokploy panels from one CLI.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Panel struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	APIKey string `json:"apiKey"`
}

type any = interface{}
type obj = map[string]any

var cfgPath = func() string { d, _ := os.UserConfigDir(); return filepath.Join(d, "dokploytools", "panels.json") }()

func die(f string, a ...any) { fmt.Fprintf(os.Stderr, "error: "+f+"\n", a...); os.Exit(1) }

func load() []Panel {
	b, err := os.ReadFile(cfgPath)
	var ps []Panel
	if err == nil {
		json.Unmarshal(b, &ps)
	}
	return ps
}

func save(ps []Panel) {
	os.MkdirAll(filepath.Dir(cfgPath), 0o700)
	b, _ := json.MarshalIndent(ps, "", "  ")
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
		die("%v", err)
	}
}

// pick returns the named panel, or all panels when name is "" / "all".
func pick(name string) []Panel {
	ps := load()
	if name == "" || name == "all" {
		if len(ps) == 0 {
			die("no panels; run: panel add")
		}
		return ps
	}
	for _, p := range ps {
		if p.Name == name {
			return []Panel{p}
		}
	}
	die("unknown panel %q", name)
	return nil
}

var hc = &http.Client{Timeout: 60 * time.Second}

// call hits /api/<path> with the panel's API key. body != nil => POST.
func (p Panel) call(method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, strings.TrimRight(p.URL, "/")+"/api/"+path, r)
	req.Header.Set("x-api-key", p.APIKey)
	req.Header.Set("Content-Type", "application/json")
	return do(req, out)
}

func do(req *http.Request, out any) error {
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

// loginKey signs in with email/password (better-auth) and mints an API key.
func loginKey(base, user, pass string) string {
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	post := func(path string, body any, out any) error {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", strings.TrimRight(base, "/")+"/api/auth/"+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", base)
		resp, err := c.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		rb, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 300 {
			return fmt.Errorf("%s: %s", resp.Status, rb)
		}
		if out != nil {
			return json.Unmarshal(rb, out)
		}
		return nil
	}
	if err := post("sign-in/email", obj{"email": user, "password": pass}, nil); err != nil {
		die("login: %v", err)
	}
	var k struct{ Key string }
	if err := post("api-key/create", obj{"name": "dokploytools"}, &k); err != nil || k.Key == "" {
		die("api-key create failed (%v); create a key in the panel and use --key", err)
	}
	return k.Key
}

func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	return fmt.Sprint(v)
}

func list(v any) []obj {
	var r []obj
	a, _ := v.([]any)
	for _, x := range a {
		if m, ok := x.(obj); ok {
			r = append(r, m)
		}
	}
	return r
}

func table(rows [][]string) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	for _, r := range rows {
		fmt.Fprintln(w, strings.Join(r, "\t"))
	}
	w.Flush()
}

// each runs fn for every selected panel, printing a header and any error.
func each(name string, fn func(Panel) error) {
	for _, p := range pick(name) {
		fmt.Printf("\n== %s (%s)\n", p.Name, p.URL)
		if err := fn(p); err != nil {
			fmt.Println("  error:", err)
		}
	}
}

// services flattens project.all into (project, kind, service) entries.
var kinds = []string{"applications", "compose", "postgres", "mysql", "mariadb", "mongo", "redis"}

func serviceStatus(s obj) string {
	for _, k := range []string{"applicationStatus", "composeStatus"} {
		if v := str(s[k]); v != "" {
			return v
		}
	}
	return str(s["applicationStatus"])
}

// active = at least one service running ("done"/"running"); idle otherwise.
func isActive(st string) bool { return st == "done" || st == "running" }

func cmdProjects(p Panel) error {
	var ps any
	if err := p.call("GET", "project.all", nil, &ps); err != nil {
		return err
	}
	rows := [][]string{{"PROJECT", "STATE", "SERVICES", "ACTIVE"}}
	for _, pr := range list(ps) {
		n, act := 0, 0
		for _, k := range kinds {
			for _, s := range list(pr[k]) {
				n++
				if isActive(serviceStatus(s)) {
					act++
				}
			}
		}
		state := "inactive"
		if act > 0 {
			state = "active"
		}
		rows = append(rows, []string{str(pr["name"]), state, fmt.Sprint(n), fmt.Sprint(act)})
	}
	table(rows)
	return nil
}

func cmdDomains(p Panel) error {
	var ps any
	if err := p.call("GET", "project.all", nil, &ps); err != nil {
		return err
	}
	rows := [][]string{{"PROJECT", "SERVICE", "DOMAIN", "STATE"}}
	for _, pr := range list(ps) {
		for _, k := range []string{"applications", "compose"} {
			for _, s := range list(pr[k]) {
				st := "inactive"
				if isActive(serviceStatus(s)) {
					st = "active"
				}
				for _, d := range list(s["domains"]) {
					scheme := "http"
					if d["https"] == true {
						scheme = "https"
					}
					rows = append(rows, []string{str(pr["name"]), str(s["name"]), scheme + "://" + str(d["host"]) + str(d["path"]), st})
				}
			}
		}
	}
	table(rows)
	return nil
}

func cmdUsers(p Panel) error {
	var us any
	if err := p.call("GET", "user.all", nil, &us); err != nil {
		return err
	}
	rows := [][]string{{"EMAIL", "ROLE", "ID"}}
	for _, u := range list(us) {
		email, id := str(u["email"]), str(u["userId"])
		if m, ok := u["user"].(obj); ok { // member rows nest the user
			email, id = str(m["email"]), str(m["id"])
		}
		rows = append(rows, []string{email, str(u["role"]), id})
	}
	table(rows)
	return nil
}

func cmdUpdate(p Panel, check bool) error {
	var cur any
	p.call("GET", "settings.getDokployVersion", nil, &cur)
	var up obj
	if err := p.call("POST", "settings.getUpdateData", obj{}, &up); err != nil {
		return err
	}
	fmt.Printf("  current=%s latest=%s updateAvailable=%v\n", str(cur), str(up["latestVersion"]), up["updateAvailable"])
	if check || up["updateAvailable"] != true {
		return nil
	}
	if err := p.call("POST", "settings.updateServer", obj{}, nil); err != nil {
		return err
	}
	fmt.Println("  update triggered (panel restarts)")
	return nil
}

func cmdMonitor(p Panel) error {
	var h any
	t0 := time.Now()
	if err := p.call("GET", "settings.health", nil, &h); err != nil {
		fmt.Println("  health: DOWN:", err)
	} else {
		fmt.Printf("  health: up (%v) %dms\n", h, time.Since(t0).Milliseconds())
	}
	var m obj
	if err := p.call("GET", "user.getMetricsToken", nil, &m); err != nil {
		return fmt.Errorf("metrics token: %w", err)
	}
	srv, _ := m["metricsConfig"].(obj)
	srv, _ = srv["server"].(obj)
	if srv == nil {
		return fmt.Errorf("monitoring not configured on panel")
	}
	u, _ := url.Parse(p.URL)
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://%s:%v/metrics?limit=1", u.Hostname(), srv["port"]), nil)
	req.Header.Set("Authorization", "Bearer "+str(srv["token"]))
	var pts []obj
	if err := do(req, &pts); err != nil || len(pts) == 0 {
		return fmt.Errorf("metrics fetch failed (is port %v open?): %v", srv["port"], err)
	}
	pt := pts[len(pts)-1]
	for _, k := range []string{"cpu", "memUsed", "memTotal", "diskUsed", "totalDisk", "networkIn", "networkOut", "uptime", "timestamp"} {
		if v, ok := pt[k]; ok {
			fmt.Printf("  %-10s %v\n", k, v)
		}
	}
	return nil
}

// cmdBackup runs a web-server backup and optionally downloads the newest file from the S3 destination.
func cmdBackup(p Panel, destID, dir string) error {
	var dests []obj
	var dl any
	if err := p.call("GET", "destination.all", nil, &dl); err != nil {
		return err
	}
	dests = list(dl)
	if len(dests) == 0 {
		return fmt.Errorf("no backup destination configured in panel")
	}
	d := dests[0]
	for _, x := range dests {
		if destID != "" && (str(x["destinationId"]) == destID || str(x["name"]) == destID) {
			d = x
		}
	}
	findBackup := func() obj {
		var u obj
		if p.call("GET", "user.getBackups", nil, &u) != nil {
			return nil
		}
		for _, b := range list(u["backups"]) {
			if str(b["databaseType"]) == "web-server" && str(b["destinationId"]) == str(d["destinationId"]) {
				return b
			}
		}
		return nil
	}
	b := findBackup()
	if b == nil {
		err := p.call("POST", "backup.create", obj{"schedule": "0 0 * * *", "enabled": false, "prefix": "dokploy-web-server",
			"destinationId": d["destinationId"], "keepLatestCount": 5, "database": "dokploy", "databaseType": "web-server"}, nil)
		if err != nil {
			return err
		}
		if b = findBackup(); b == nil {
			return fmt.Errorf("created backup config but cannot find it")
		}
	}
	if err := p.call("POST", "backup.manualBackupWebServer", obj{"backupId": b["backupId"]}, nil); err != nil {
		return err
	}
	fmt.Println("  backup triggered to destination", str(d["name"]))
	if dir == "" {
		return nil
	}
	ep := str(d["endpoint"])
	secure := !strings.HasPrefix(ep, "http://")
	ep = strings.TrimPrefix(strings.TrimPrefix(ep, "https://"), "http://")
	mc, err := minio.New(ep, &minio.Options{Creds: credentials.NewStaticV4(str(d["accessKey"]), str(d["secretAccessKey"]), ""),
		Secure: secure, Region: str(d["region"])})
	if err != nil {
		return err
	}
	ctx := context.Background()
	var newest minio.ObjectInfo
	// the backup runs async on the panel; poll up to ~2 min for a file newer than now-1m.
	start := time.Now().Add(-time.Minute)
	for i := 0; i < 24; i++ {
		newest = minio.ObjectInfo{}
		for o := range mc.ListObjects(ctx, str(d["bucket"]), minio.ListObjectsOptions{Prefix: str(b["prefix"]), Recursive: true}) {
			if o.Err == nil && o.LastModified.After(newest.LastModified) {
				newest = o
			}
		}
		if newest.LastModified.After(start) {
			break
		}
		time.Sleep(5 * time.Second)
	}
	if newest.Key == "" {
		return fmt.Errorf("no backup file found in bucket")
	}
	os.MkdirAll(dir, 0o755)
	dst := filepath.Join(dir, p.Name+"-"+filepath.Base(newest.Key))
	if err := mc.FGetObject(ctx, str(d["bucket"]), newest.Key, dst, minio.GetObjectOptions{}); err != nil {
		return err
	}
	fmt.Println("  downloaded", dst)
	return nil
}

func flags(args []string) (pos []string, f map[string]string) {
	f = map[string]string{}
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") {
			k := args[i][2:]
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				f[k] = args[i+1]
				i++
			} else {
				f[k] = "true"
			}
		} else {
			pos = append(pos, args[i])
		}
	}
	return
}

const usage = `dokploytools — manage multiple Dokploy panels

  panel add NAME URL (--key KEY | --user EMAIL --pass PASS)
  panel list | panel edit NAME [--url U] [--key K | --user E --pass P] | panel rm NAME
  update   [NAME|all] [--check]     update Dokploy (all panels by default)
  projects [NAME]                   projects with active/inactive state
  domains  [NAME]                   domains per project
  users    [NAME]                   panel users
  backup   [NAME] [--dest ID|NAME] [--download DIR]
  monitor  [NAME]                   health + CPU/mem/disk metrics
  raw NAME METHOD PATH [JSON]       raw API call, e.g. raw prod GET project.all

NAME omitted = all panels. Config: ` + "~/.config/dokploytools/panels.json"

func main() {
	pos, f := flags(os.Args[1:])
	if len(pos) == 0 {
		fmt.Println(usage)
		return
	}
	arg := func(i int) string {
		if len(pos) > i {
			return pos[i]
		}
		return ""
	}
	cred := func(url string) string {
		if f["key"] != "" {
			return f["key"]
		}
		if f["user"] != "" {
			return loginKey(url, f["user"], f["pass"])
		}
		die("need --key or --user/--pass")
		return ""
	}
	switch pos[0] {
	case "panel":
		ps := load()
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
			for _, p := range ps {
				rows = append(rows, []string{p.Name, p.URL, "***" + p.APIKey[max(0, len(p.APIKey)-4):]})
			}
			table(rows)
		case "edit":
			for i := range ps {
				if ps[i].Name == arg(2) {
					if f["url"] != "" {
						ps[i].URL = f["url"]
					}
					if f["key"] != "" || f["user"] != "" {
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
		default:
			die("panel add|list|edit|rm")
		}
	case "update":
		each(arg(1), func(p Panel) error { return cmdUpdate(p, f["check"] != "") })
	case "projects":
		each(arg(1), cmdProjects)
	case "domains":
		each(arg(1), cmdDomains)
	case "users":
		each(arg(1), cmdUsers)
	case "monitor":
		each(arg(1), cmdMonitor)
	case "backup":
		each(arg(1), func(p Panel) error { return cmdBackup(p, f["dest"], f["download"]) })
	case "raw":
		p := pick(arg(1))[0]
		var body any
		if arg(4) != "" {
			json.Unmarshal([]byte(arg(4)), &body)
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
