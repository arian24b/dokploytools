package main

import (
	"fmt"
	"os"
	"path"
	"strings"
	"time"
)

type svc struct {
	Project, ProjectID, Env, EnvID, Kind, ID, Name, AppName, Status string
	Raw                                                             obj
}

var kinds = []struct{ key, kind string }{{"applications", "application"}, {"compose", "compose"}, {"postgres", "postgres"},
	{"mysql", "mysql"}, {"mariadb", "mariadb"}, {"mongo", "mongo"}, {"redis", "redis"}}

func isDB(kind string) bool { return kind != "application" && kind != "compose" }

// active = deployed/running.
func isActive(st string) bool { return st == "done" || st == "running" }

func state(st string) string {
	if isActive(st) {
		return "active"
	}
	return "inactive"
}

func projects(p Panel) ([]obj, error) {
	var v any
	err := p.call("GET", "project.all", nil, &v)
	return list(v), err
}

// services flattens project.all (project -> environments -> services).
// Older Dokploy has no environments; services then sit on the project itself.
func servicesOf(ps []obj) []svc {
	var out []svc
	for _, pr := range ps {
		envs := list(pr["environments"])
		legacy := len(envs) == 0
		if legacy {
			envs = []obj{pr}
		}
		for _, e := range envs {
			en := ""
			if !legacy {
				en = str(e["name"])
			}
			for _, k := range kinds {
				for _, s := range list(e[k.key]) {
					st := str(s["applicationStatus"])
					if k.kind == "compose" {
						st = str(s["composeStatus"])
					}
					out = append(out, svc{str(pr["name"]), str(pr["projectId"]), en, str(e["environmentId"]), k.kind,
						str(s[k.kind+"Id"]), str(s["name"]), str(s["appName"]), st, s})
				}
			}
		}
	}
	return out
}

func services(p Panel) ([]svc, error) {
	ps, err := projects(p)
	return servicesOf(ps), err
}

// resolve finds PROJECT/SERVICE (prefers the "production" env when ambiguous).
func resolve(p Panel, ref, env string) (svc, error) {
	proj, name, _ := strings.Cut(ref, "/")
	if name == "" {
		return svc{}, fmt.Errorf("service must be PROJECT/SERVICE, got %q", ref)
	}
	all, err := services(p)
	if err != nil {
		return svc{}, err
	}
	var m []svc
	for _, s := range all {
		if s.Project == proj && s.Name == name && (env == "" || s.Env == env) {
			m = append(m, s)
		}
	}
	if len(m) > 1 && env == "" {
		for _, s := range m {
			if s.Env == "production" {
				return s, nil
			}
		}
	}
	switch len(m) {
	case 0:
		return svc{}, fmt.Errorf("service %q not found", ref)
	case 1:
		return m[0], nil
	}
	return svc{}, fmt.Errorf("%q is ambiguous across environments; use --env", ref)
}

func one(p Panel, s svc) (obj, error) {
	var o obj
	err := p.call("GET", s.Kind+".one", obj{s.Kind + "Id": s.ID}, &o)
	return o, err
}

func cmdProjects(p Panel) error {
	all, err := services(p)
	if err != nil {
		return err
	}
	ps, _ := projects(p)
	rows := [][]string{{"PROJECT", "STATE", "SERVICES", "ACTIVE"}}
	for _, pr := range ps {
		n, act := 0, 0
		for _, s := range all {
			if s.ProjectID == str(pr["projectId"]) {
				n++
				if isActive(s.Status) {
					act++
				}
			}
		}
		st := "inactive"
		if act > 0 {
			st = "active"
		}
		rows = append(rows, []string{str(pr["name"]), st, fmt.Sprint(n), fmt.Sprint(act)})
	}
	table(rows)
	return nil
}

func cmdStatus(p Panel, f F) error {
	all, err := services(p)
	if err != nil {
		return err
	}
	rows := [][]string{{"PROJECT", "ENV", "KIND", "SERVICE", "STATUS", "STATE", "ID"}}
	for _, s := range all {
		if pj := f.s("project"); pj != "" && s.Project != pj {
			continue
		}
		rows = append(rows, []string{s.Project, s.Env, s.Kind, s.Name, s.Status, state(s.Status), s.ID})
	}
	table(rows)
	return nil
}

func deployments(p Panel, s svc) ([]obj, error) {
	var v any
	var err error
	switch s.Kind {
	case "application":
		err = p.call("GET", "deployment.all", obj{"applicationId": s.ID}, &v)
	case "compose":
		err = p.call("GET", "deployment.allByCompose", obj{"composeId": s.ID}, &v)
	default:
		return nil, fmt.Errorf("deployments only exist for application/compose")
	}
	d := list(v)
	newestFirst(d, "createdAt")
	return d, err
}

func cmdDeployments(p Panel, ref string, f F) error {
	s, err := resolve(p, ref, f.s("env"))
	if err != nil {
		return err
	}
	ds, err := deployments(p, s)
	if err != nil {
		return err
	}
	rows := [][]string{{"ID", "STATUS", "TITLE", "CREATED"}}
	for i, d := range ds {
		if i >= f.n("limit", 10) {
			break
		}
		rows = append(rows, []string{str(d["deploymentId"]), str(d["status"]), str(d["title"]), str(d["createdAt"])})
	}
	table(rows)
	return nil
}

// waitDeploy blocks until a deployment newer than `before` finishes.
func waitDeploy(p Panel, s svc, before string, timeout time.Duration) error {
	end := time.Now().Add(timeout)
	for time.Now().Before(end) {
		time.Sleep(4 * time.Second)
		ds, err := deployments(p, s)
		if err != nil || len(ds) == 0 || str(ds[0]["deploymentId"]) == before {
			continue
		}
		switch str(ds[0]["status"]) {
		case "done":
			return nil
		case "error":
			return fmt.Errorf("deployment failed (id %s)", str(ds[0]["deploymentId"]))
		}
	}
	return fmt.Errorf("timed out waiting for deployment")
}

func latestDeployID(p Panel, s svc) string {
	if ds, err := deployments(p, s); err == nil && len(ds) > 0 {
		return str(ds[0]["deploymentId"])
	}
	return ""
}

// cmdSvc runs start|stop|deploy|redeploy on one service.
func cmdSvc(p Panel, action, ref string, f F) error {
	s, err := resolve(p, ref, f.s("env"))
	if err != nil {
		return err
	}
	switch action {
	case "start", "stop", "deploy":
	case "redeploy":
		if isDB(s.Kind) {
			return fmt.Errorf("redeploy is only for application/compose; use deploy")
		}
	default:
		return fmt.Errorf("action must be start|stop|deploy|redeploy")
	}
	body := obj{s.Kind + "Id": s.ID}
	if s.Kind == "application" || s.Kind == "compose" {
		if action == "deploy" || action == "redeploy" {
			body["title"] = "dokploytools " + action
		}
	}
	before := ""
	if f.has("wait") && !isDB(s.Kind) {
		before = latestDeployID(p, s)
	}
	if err := p.call("POST", s.Kind+"."+action, body, nil); err != nil {
		return err
	}
	if f.has("wait") && (action == "deploy" || action == "redeploy") && !isDB(s.Kind) {
		if err := waitDeploy(p, s, before, time.Duration(f.n("timeout", 600))*time.Second); err != nil {
			return err
		}
	}
	done("%s %s/%s ok", action, s.Project, s.Name)
	return nil
}

// mergeEnv applies KEY=VAL sets and key unsets to a dotenv-style string.
func mergeEnv(cur string, set, unset []string) string {
	var lines []string
	if cur != "" {
		lines = strings.Split(strings.TrimRight(cur, "\n"), "\n")
	}
	idx := func(k string) int {
		for i, l := range lines {
			if strings.HasPrefix(l, k+"=") {
				return i
			}
		}
		return -1
	}
	for _, kv := range set {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if i := idx(k); i >= 0 {
			lines[i] = kv
		} else {
			lines = append(lines, kv)
		}
	}
	for _, k := range unset {
		if i := idx(k); i >= 0 {
			lines = append(lines[:i], lines[i+1:]...)
		}
	}
	return strings.Join(lines, "\n")
}

func saveEnv(p Panel, s svc, cur obj, env string) error {
	switch s.Kind {
	case "application": // saveEnvironment replaces build args/secrets too, so pass them through
		cef := true
		if b, ok := cur["createEnvFile"].(bool); ok {
			cef = b
		}
		return p.call("POST", "application.saveEnvironment", obj{"applicationId": s.ID, "env": env,
			"buildArgs": cur["buildArgs"], "buildSecrets": cur["buildSecrets"], "createEnvFile": cef}, nil)
	case "compose":
		return p.call("POST", "compose.update", obj{"composeId": s.ID, "env": env}, nil)
	}
	return p.call("POST", s.Kind+".saveEnvironment", obj{s.Kind + "Id": s.ID, "env": env}, nil)
}

// cmdEnv: env PANEL REF [set K=V.. | unset K.. | --file F]
func cmdEnv(p Panel, ref string, args []string, f F) error {
	s, err := resolve(p, ref, f.s("env"))
	if err != nil {
		return err
	}
	cur, err := one(p, s)
	if err != nil {
		return err
	}
	old := str(cur["env"])
	if len(args) == 0 && !f.has("file") {
		for _, l := range strings.Split(old, "\n") {
			if l != "" {
				table([][]string{{"LINE"}, {l}})
			}
		}
		return nil
	}
	env := old
	if file := f.s("file"); file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		env = strings.TrimRight(string(b), "\n")
	} else if args[0] == "set" {
		env = mergeEnv(old, args[1:], nil)
	} else if args[0] == "unset" {
		env = mergeEnv(old, nil, args[1:])
	} else {
		return fmt.Errorf("env PANEL REF [set K=V.. | unset K.. | --file F]")
	}
	if err := saveEnv(p, s, cur, env); err != nil {
		return err
	}
	done("env saved for %s/%s (redeploy to apply)", s.Project, s.Name)
	return nil
}

func cmdServers(p Panel, f F) error {
	var v any
	if err := p.call("GET", "server.all", nil, &v); err != nil {
		return err
	}
	rows := [][]string{{"NAME", "IP", "PORT", "TYPE", "STATUS", "ID", "CHECK"}}
	for _, s := range list(v) {
		chk := ""
		if f.has("check") {
			var r any
			if err := p.call("GET", "server.validate", obj{"serverId": s["serverId"]}, &r); err != nil {
				chk = "error: " + err.Error()
			} else {
				chk = compact(r)
			}
		}
		rows = append(rows, []string{str(s["name"]), str(s["ipAddress"]), str(s["port"]), str(s["serverType"]),
			str(s["serverStatus"]), str(s["serverId"]), chk})
	}
	table(rows)
	return nil
}

func cmdContainers(p Panel, f F) error {
	if f.has("stats") {
		return containerStats(p)
	}
	var v any
	if err := p.call("GET", "docker.getContainers", obj{"serverId": f.s("server")}, &v); err != nil {
		return err
	}
	rows := [][]string{{"NAME", "IMAGE", "STATE", "STATUS", "ID"}}
	for _, c := range list(v) {
		rows = append(rows, []string{str(c["name"]), str(c["image"]), str(c["state"]), str(c["status"]), str(c["containerId"])})
	}
	table(rows)
	return nil
}

// containerStats: per-service CPU/mem from the panel's monitoring service (needs monitoring enabled).
func containerStats(p Panel) error {
	base, token, err := metricsConn(p)
	if err != nil {
		return err
	}
	all, err := services(p)
	if err != nil {
		return err
	}
	rows := [][]string{{"PROJECT", "SERVICE", "USAGE"}}
	for _, s := range all {
		var v any
		err := p.call("GET", "user.getContainerMetrics", obj{"url": base + "/metrics/containers", "token": token,
			"appName": s.AppName, "dataPoints": "1"}, &v)
		use := ""
		if err != nil {
			use = "n/a"
		} else if pts, ok := v.([]any); ok && len(pts) > 0 {
			use = compact(pts[len(pts)-1])
		} else {
			use = compact(v)
		}
		rows = append(rows, []string{s.Project, s.Name, use})
	}
	table(rows)
	return nil
}

// cmdDeploy: one-liner git/compose deploy; creates project/env/compose as needed.
func cmdDeploy(p Panel, f F) error {
	repo, file := f.s("repo"), f.s("file")
	if repo == "" && file == "" {
		return fmt.Errorf("need --repo URL (or --file compose.yml)")
	}
	name := f.s("name")
	if name == "" && repo != "" {
		name = strings.TrimSuffix(path.Base(strings.TrimSuffix(repo, "/")), ".git")
	}
	if name == "" {
		return fmt.Errorf("need --name with --file")
	}
	projName, envName := f.or("project", name), f.or("env", "production")

	findProj := func() (obj, error) {
		ps, err := projects(p)
		for _, pr := range ps {
			if str(pr["name"]) == projName {
				return pr, err
			}
		}
		return nil, err
	}
	pr, err := findProj()
	if err != nil {
		return err
	}
	if pr == nil {
		if err := p.call("POST", "project.create", obj{"name": projName, "description": "created by dokploytools"}, nil); err != nil {
			return err
		}
		if pr, err = findProj(); err != nil || pr == nil {
			return fmt.Errorf("project %q not found after create (%v)", projName, err)
		}
	}
	pickEnv := func(pr obj) obj {
		envs := list(pr["environments"])
		for _, e := range envs {
			if str(e["name"]) == envName {
				return e
			}
		}
		if len(envs) > 0 && !f.has("env") {
			return envs[0]
		}
		return nil
	}
	env := pickEnv(pr)
	if env == nil && len(list(pr["environments"])) > 0 { // named env missing: create it
		if err := p.call("POST", "environment.create", obj{"name": envName, "projectId": pr["projectId"]}, nil); err != nil {
			return err
		}
		if pr, err = findProj(); err != nil || pr == nil {
			return fmt.Errorf("project vanished: %v", err)
		}
		env = pickEnv(pr)
	}
	if env == nil {
		return fmt.Errorf("no environment found in project %q (panel too old?)", projName)
	}

	var compose obj
	for _, c := range list(env["compose"]) {
		if str(c["name"]) == name {
			compose = c
		}
	}
	id := str(compose["composeId"])
	if id == "" {
		body := obj{"name": name, "environmentId": env["environmentId"], "composeType": "docker-compose", "sourceType": "git"}
		if file != "" {
			body["sourceType"] = "raw"
		}
		if sv := f.s("server"); sv != "" {
			body["serverId"] = sv
		}
		var created obj
		if err := p.call("POST", "compose.create", body, &created); err != nil {
			return err
		}
		if id = str(created["composeId"]); id == "" {
			all, _ := services(p)
			for _, s := range all {
				if s.Kind == "compose" && s.Name == name && s.EnvID == str(env["environmentId"]) {
					id = s.ID
				}
			}
		}
		if id == "" {
			return fmt.Errorf("compose created but id not returned")
		}
	}
	s := svc{Project: projName, Kind: "compose", ID: id, Name: name}

	upd := obj{"composeId": id}
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		upd["sourceType"], upd["composeFile"] = "raw", string(b)
	} else {
		upd["sourceType"], upd["customGitUrl"] = "git", repo
		upd["customGitBranch"] = f.or("branch", "main")
		upd["composePath"] = f.or("compose-path", "./docker-compose.yml")
		upd["autoDeploy"] = f.has("auto-deploy")
		if k := f.s("ssh-key-id"); k != "" {
			upd["customGitSSHKeyId"] = k
		}
	}
	if sets := f.all("set"); len(sets) > 0 || f.has("env-file") {
		cur, _ := one(p, s)
		e := str(cur["env"])
		if ef := f.s("env-file"); ef != "" {
			b, err := os.ReadFile(ef)
			if err != nil {
				return err
			}
			e = mergeEnv(e, strings.Split(strings.TrimSpace(string(b)), "\n"), nil)
		}
		upd["env"] = mergeEnv(e, sets, nil)
	}
	if err := p.call("POST", "compose.update", upd, nil); err != nil {
		return fmt.Errorf("compose.update: %w", err)
	}

	if host := f.s("domain"); host != "" {
		if f.s("service") == "" || f.s("port") == "" {
			return fmt.Errorf("--domain needs --service and --port")
		}
		have := false
		cur, _ := one(p, s)
		for _, d := range list(cur["domains"]) {
			have = have || str(d["host"]) == host
		}
		if !have {
			if err := addDomain(p, s, host, f); err != nil {
				return fmt.Errorf("domain: %w", err)
			}
		}
	}

	if f.has("no-deploy") {
		done("configured %s/%s/%s (id %s), not deployed", projName, str(env["name"]), name, id)
		return nil
	}
	before := latestDeployID(p, s)
	if err := p.call("POST", "compose.deploy", obj{"composeId": id, "title": "dokploytools deploy"}, nil); err != nil {
		return err
	}
	if f.has("wait") {
		if err := waitDeploy(p, s, before, time.Duration(f.n("timeout", 900))*time.Second); err != nil {
			return err
		}
	}
	done("deployed %s/%s/%s (id %s)", projName, str(env["name"]), name, id)
	return nil
}
