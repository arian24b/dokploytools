package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// ---- domains ----

func domainsOf(p Panel, s svc) []obj {
	if d, ok := s.Raw["domains"]; ok {
		return list(d)
	}
	var v any
	if s.Kind == "application" {
		p.call("GET", "domain.byApplicationId", obj{"applicationId": s.ID}, &v)
	} else if s.Kind == "compose" {
		p.call("GET", "domain.byComposeId", obj{"composeId": s.ID}, &v)
	}
	return list(v)
}

func domainURL(d obj) string {
	scheme := "http"
	if d["https"] == true {
		scheme = "https"
	}
	return scheme + "://" + str(d["host"]) + str(d["path"])
}

func cmdDomains(p Panel) error {
	all, err := services(p)
	if err != nil {
		return err
	}
	rows := [][]string{{"PROJECT", "SERVICE", "DOMAIN", "STATE"}}
	for _, s := range all {
		for _, d := range domainsOf(p, s) {
			rows = append(rows, []string{s.Project, s.Name, domainURL(d), state(s.Status)})
		}
	}
	table(rows)
	return nil
}

func addDomain(p Panel, s svc, host string, f F) error {
	https := !f.has("no-https")
	cert := f.s("cert")
	if cert == "" {
		cert = "none"
		if https {
			cert = "letsencrypt"
		}
	}
	port, err := strconv.Atoi(f.s("port"))
	if err != nil {
		return fmt.Errorf("--port is required")
	}
	body := obj{"host": host, "https": https, "certificateType": cert, "port": port, "path": f.or("path", "/"), "stripPath": false}
	if r := f.s("resolver"); r != "" {
		body["customCertResolver"] = r
	}
	switch s.Kind {
	case "compose":
		if f.s("service") == "" {
			return fmt.Errorf("--service is required for compose")
		}
		body["composeId"], body["serviceName"], body["domainType"] = s.ID, f.s("service"), "compose"
	case "application":
		body["applicationId"], body["domainType"] = s.ID, "application"
	default:
		return fmt.Errorf("domains are for application/compose")
	}
	return p.call("POST", "domain.create", body, nil)
}

// probeTLS dials host:443 and reports the served leaf cert.
func probeTLS(host string) (*x509.Certificate, error) {
	c, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", host+":443",
		&tls.Config{InsecureSkipVerify: true, ServerName: host})
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.ConnectionState().PeerCertificates[0], nil
}

func daysLeft(t time.Time) int { return int(time.Until(t).Hours() / 24) }

// cmdDomain: domain add PANEL REF --host H --port N | rm PANEL HOST | check PANEL [HOST]
func cmdDomain(p Panel, args []string, f F) error {
	arg := func(i int) string {
		if len(args) > i {
			return args[i]
		}
		return ""
	}
	switch arg(0) {
	case "add":
		s, err := resolve(p, arg(1), f.s("env"))
		if err != nil {
			return err
		}
		if f.s("host") == "" {
			return fmt.Errorf("--host is required")
		}
		if err := addDomain(p, s, f.s("host"), f); err != nil {
			return err
		}
		done("domain %s added to %s/%s", f.s("host"), s.Project, s.Name)
	case "rm":
		all, err := services(p)
		if err != nil {
			return err
		}
		for _, s := range all {
			for _, d := range domainsOf(p, s) {
				if str(d["host"]) == arg(1) {
					if err := p.call("POST", "domain.delete", obj{"domainId": d["domainId"]}, nil); err != nil {
						return err
					}
					done("domain %s removed from %s/%s", arg(1), s.Project, s.Name)
					return nil
				}
			}
		}
		return fmt.Errorf("domain %q not found", arg(1))
	case "check": // DNS check by the panel + live TLS expiry
		all, err := services(p)
		if err != nil {
			return err
		}
		rows := [][]string{{"DOMAIN", "DNS", "TLS_EXPIRES", "DAYS", "ISSUER"}}
		for _, s := range all {
			for _, d := range domainsOf(p, s) {
				host := str(d["host"])
				if arg(1) != "" && host != arg(1) {
					continue
				}
				dns := ""
				var v any
				if err := p.call("POST", "domain.validateDomain", obj{"domain": host}, &v); err != nil {
					dns = "error: " + err.Error()
				} else {
					dns = compact(v)
				}
				exp, days, iss := "-", "-", "-"
				if c, err := probeTLS(host); err != nil {
					exp = "no TLS: " + err.Error()
				} else {
					exp, days, iss = c.NotAfter.Format("2006-01-02"), strconv.Itoa(daysLeft(c.NotAfter)), c.Issuer.CommonName
				}
				rows = append(rows, []string{host, dns, exp, days, iss})
			}
		}
		table(rows)
	default:
		return fmt.Errorf("domain add|rm|check")
	}
	return nil
}

// ---- certificates (custom certs for domains whose DNS is not on this server) ----

func certMeta(pemData string) (names string, notAfter time.Time) {
	blk, _ := pem.Decode([]byte(pemData))
	if blk == nil {
		return "", time.Time{}
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return "", time.Time{}
	}
	return strings.Join(c.DNSNames, ","), c.NotAfter
}

// cmdCert: cert list | add NAME --cert F --key F [--domain H] | rm ID|NAME
func cmdCert(p Panel, args []string, f F) error {
	arg := func(i int) string {
		if len(args) > i {
			return args[i]
		}
		return ""
	}
	var all any
	if err := p.call("GET", "certificates.all", nil, &all); err != nil {
		return err
	}
	switch arg(0) {
	case "list":
		rows := [][]string{{"NAME", "NAMES", "EXPIRES", "DAYS", "AUTORENEW", "ID"}}
		for _, c := range list(all) {
			names, na := certMeta(str(c["certificateData"]))
			exp, days := "?", "?"
			if !na.IsZero() {
				exp, days = na.Format("2006-01-02"), strconv.Itoa(daysLeft(na))
			}
			rows = append(rows, []string{str(c["name"]), names, exp, days, str(c["autoRenew"]), str(c["certificateId"])})
		}
		table(rows)
	case "add":
		if arg(1) == "" || f.s("cert") == "" || f.s("key") == "" {
			return fmt.Errorf("cert add NAME --cert fullchain.pem --key privkey.pem [--domain H] [--auto-renew]")
		}
		cb, err := os.ReadFile(f.s("cert"))
		if err != nil {
			return err
		}
		kb, err := os.ReadFile(f.s("key"))
		if err != nil {
			return err
		}
		pair, err := tls.X509KeyPair(cb, kb)
		if err != nil {
			return fmt.Errorf("cert/key do not match: %w", err)
		}
		leaf, _ := x509.ParseCertificate(pair.Certificate[0])
		if time.Now().After(leaf.NotAfter) {
			return fmt.Errorf("certificate expired %s", leaf.NotAfter.Format("2006-01-02"))
		}
		if d := f.s("domain"); d != "" {
			if err := leaf.VerifyHostname(d); err != nil {
				return fmt.Errorf("certificate does not cover %s: %w", d, err)
			}
		}
		org := f.s("org")
		if org == "" {
			var os_ any
			if err := p.call("GET", "organization.all", nil, &os_); err == nil {
				if l := list(os_); len(l) > 0 {
					org = str(l[0]["id"])
					if org == "" {
						org = str(l[0]["organizationId"])
					}
				}
			}
		}
		body := obj{"name": arg(1), "certificateData": string(cb), "privateKey": string(kb), "organizationId": org, "autoRenew": f.has("auto-renew")}
		if sv := f.s("server"); sv != "" {
			body["serverId"] = sv
		}
		if err := p.call("POST", "certificates.create", body, nil); err != nil {
			return err
		}
		done("certificate %s added (covers %v, expires %s). Use domains with --cert none; Traefik picks it by SNI", arg(1),
			leaf.DNSNames, leaf.NotAfter.Format("2006-01-02"))
	case "rm":
		for _, c := range list(all) {
			if str(c["certificateId"]) == arg(1) || str(c["name"]) == arg(1) {
				if err := p.call("POST", "certificates.remove", obj{"certificateId": c["certificateId"]}, nil); err != nil {
					return err
				}
				done("certificate %s removed", str(c["name"]))
				return nil
			}
		}
		return fmt.Errorf("certificate %q not found", arg(1))
	default:
		return fmt.Errorf("cert list|add|rm")
	}
	return nil
}

// ---- traefik ----

var traefikEP = map[string][2]string{
	"main":       {"settings.readTraefikConfig", "settings.updateTraefikConfig"},
	"web":        {"settings.readWebServerTraefikConfig", "settings.updateWebServerTraefikConfig"},
	"middleware": {"settings.readMiddlewareTraefikConfig", "settings.updateMiddlewareTraefikConfig"},
}

// cmdTraefik: traefik get|set PANEL main|web|middleware|--path P ; ls ; reload
func cmdTraefik(p Panel, args []string, f F) error {
	arg := func(i int) string {
		if len(args) > i {
			return args[i]
		}
		return ""
	}
	sid := obj{}
	if s := f.s("server"); s != "" {
		sid["serverId"] = s
	}
	read := func() (string, error) {
		var v any
		var err error
		if pa := f.s("path"); pa != "" {
			q := obj{"path": pa}
			for k, x := range sid {
				q[k] = x
			}
			err = p.call("GET", "settings.readTraefikFile", q, &v)
		} else if ep, ok := traefikEP[arg(1)]; ok {
			err = p.call("GET", ep[0], nil, &v)
		} else {
			return "", fmt.Errorf("pick main|web|middleware or --path FILE")
		}
		return compact(v), err
	}
	switch arg(0) {
	case "get":
		s, err := read()
		if err != nil {
			return err
		}
		if jsonOut {
			jsonRows = append(jsonRows, obj{"panel": curPanel, "content": s})
		} else {
			fmt.Println(s)
		}
	case "ls":
		var v any
		if err := p.call("GET", "settings.readDirectories", sid, &v); err != nil {
			return err
		}
		table([][]string{{"TREE"}, {compact(v)}})
	case "set":
		src := f.s("from")
		if src == "" {
			return fmt.Errorf("traefik set ... --from FILE")
		}
		nb, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		old, err := read()
		if err != nil {
			return err
		}
		bak := fmt.Sprintf("traefik-%s-%s-%d.bak", p.Name, strings.ReplaceAll(arg(1)+f.s("path"), "/", "_"), time.Now().Unix())
		if err := os.WriteFile(bak, []byte(old), 0o600); err != nil {
			return err
		}
		if pa := f.s("path"); pa != "" {
			body := obj{"path": pa, "traefikConfig": string(nb)}
			for k, x := range sid {
				body[k] = x
			}
			err = p.call("POST", "settings.updateTraefikFile", body, nil)
		} else {
			err = p.call("POST", traefikEP[arg(1)][1], obj{"traefikConfig": string(nb)}, nil)
		}
		if err != nil {
			return err
		}
		done("traefik config updated (previous saved to %s)", bak)
	case "reload":
		if err := p.call("POST", "settings.reloadTraefik", sid, nil); err != nil {
			return err
		}
		done("traefik reloaded")
	default:
		return fmt.Errorf("traefik get|set|ls|reload")
	}
	return nil
}

// ---- update / monitor / alert ----

func cmdUpdate(p Panel, check bool) error {
	var cur any
	p.call("GET", "settings.getDokployVersion", nil, &cur)
	var up obj
	if err := p.call("POST", "settings.getUpdateData", obj{}, &up); err != nil {
		return err
	}
	act := "none"
	if up["updateAvailable"] == true && !check {
		if err := p.call("POST", "settings.updateServer", obj{}, nil); err != nil {
			return err
		}
		act = "update triggered (panel restarts)"
	}
	table([][]string{{"CURRENT", "LATEST", "AVAILABLE", "ACTION"},
		{str(cur), str(up["latestVersion"]), str(up["updateAvailable"]), act}})
	return nil
}

func metricsConn(p Panel) (base, token string, err error) {
	var m obj
	if err = p.call("GET", "user.getMetricsToken", nil, &m); err != nil {
		return "", "", fmt.Errorf("metrics token: %w", err)
	}
	srv, _ := sub(m, "metricsConfig", "server").(obj)
	if srv == nil {
		return "", "", fmt.Errorf("monitoring not configured on panel")
	}
	u, _ := url.Parse(p.URL)
	return fmt.Sprintf("http://%s:%v", u.Hostname(), srv["port"]), str(srv["token"]), nil
}

func lastMetric(p Panel) (obj, error) {
	base, token, err := metricsConn(p)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequest("GET", base+"/metrics?limit=1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	var pts []obj
	if err := do(req, &pts); err != nil || len(pts) == 0 {
		return nil, fmt.Errorf("metrics fetch failed (is %s reachable?): %v", base, err)
	}
	return pts[len(pts)-1], nil
}

func cmdMonitor(p Panel) error {
	var ver any
	t0 := time.Now()
	if err := p.call("GET", "settings.getDokployVersion", nil, &ver); err != nil {
		table([][]string{{"HEALTH"}, {"DOWN: " + err.Error()}})
		return nil
	}
	pt, err := lastMetric(p)
	row := obj{"health": fmt.Sprintf("up v%v %dms", ver, time.Since(t0).Milliseconds())}
	if err != nil {
		table([][]string{{"HEALTH", "METRICS"}, {row["health"].(string), err.Error()}})
		return nil
	}
	h := []string{"HEALTH"}
	r := []string{row["health"].(string)}
	for _, k := range []string{"cpu", "memUsed", "memUsedGB", "memTotal", "diskUsed", "totalDisk", "networkIn", "networkOut", "uptime"} {
		if v, ok := pt[k]; ok {
			h, r = append(h, strings.ToUpper(k)), append(r, str(v))
		}
	}
	table([][]string{h, r})
	return nil
}

func notify(f F, msg string) {
	if u := f.s("webhook"); u != "" {
		b, _ := json.Marshal(obj{"text": msg, "content": msg})
		hc.Post(u, "application/json", bytes.NewReader(b))
	}
	if t, c, ok := strings.Cut(f.s("telegram"), "@"); ok { // TOKEN@CHATID
		hc.PostForm("https://api.telegram.org/bot"+t+"/sendMessage", url.Values{"chat_id": {c}, "text": {msg}})
	}
}

// cmdAlert exits non-zero (via failed) when any check trips.
func cmdAlert(p Panel, f F) error {
	var msgs []string
	bad := func(s string, a ...any) { msgs = append(msgs, fmt.Sprintf(s, a...)) }
	var ver any
	if err := p.call("GET", "settings.getDokployVersion", nil, &ver); err != nil {
		bad("panel DOWN: %v", err)
	} else {
		if all, err := services(p); err == nil {
			for _, s := range all {
				if s.Status == "error" {
					bad("service %s/%s is in error state", s.Project, s.Name)
				}
				if !f.has("no-certs") {
					for _, d := range domainsOf(p, s) {
						if d["https"] != true || !isActive(s.Status) {
							continue
						}
						h := str(d["host"])
						if c, err := probeTLS(h); err != nil {
							bad("domain %s: TLS check failed: %v", h, err)
						} else if dl := daysLeft(c.NotAfter); dl < f.n("cert-days", 14) {
							bad("domain %s: certificate expires in %d days", h, dl)
						}
					}
				}
			}
		}
		var sv any
		if p.call("GET", "server.all", nil, &sv) == nil {
			for _, s := range list(sv) {
				if st := str(s["serverStatus"]); st != "" && st != "active" {
					bad("server %s is %s", str(s["name"]), st)
				}
			}
		}
		if pt, err := lastMetric(p); err == nil {
			for _, c := range []struct {
				key, flag string
				def       int
			}{{"cpu", "cpu", 90}, {"memUsed", "mem", 90}, {"diskUsed", "disk", 90}} {
				if v, lim := num(pt[c.key]), float64(f.n(c.flag, c.def)); v > lim {
					bad("%s at %.1f%% (limit %.0f%%)", c.key, v, lim)
				}
			}
		} else if f.has("require-metrics") {
			bad("metrics unavailable: %v", err)
		}
	}
	rows := [][]string{{"LEVEL", "MESSAGE"}}
	for _, m := range msgs {
		rows = append(rows, []string{"ALERT", m})
	}
	if len(msgs) == 0 {
		rows = append(rows, []string{"OK", "all checks passed"})
	}
	table(rows)
	if len(msgs) > 0 {
		failed = true
		notify(f, p.Name+": "+strings.Join(msgs, "; "))
	}
	return nil
}

// ---- members, api keys, orgs ----

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

// cmdMember: member invite EMAIL [--role] | rm USERID | invitations | uninvite ID
func cmdMember(p Panel, args []string, f F) error {
	arg := func(i int) string {
		if len(args) > i {
			return args[i]
		}
		return ""
	}
	switch arg(0) {
	case "invite": // better-auth organization plugin route
		body := obj{"email": arg(1), "role": f.or("role", "member")}
		if o := f.s("org"); o != "" {
			body["organizationId"] = o
		}
		if err := p.call("POST", "auth/organization/invite-member", body, nil); err != nil {
			return err
		}
		done("invited %s as %s", arg(1), body["role"])
	case "rm":
		if err := p.call("POST", "user.remove", obj{"userId": arg(1)}, nil); err != nil {
			return err
		}
		done("user %s removed", arg(1))
	case "invitations":
		var v any
		if err := p.call("GET", "organization.allInvitations", nil, &v); err != nil {
			return err
		}
		rows := [][]string{{"EMAIL", "ROLE", "STATUS", "ID"}}
		for _, i := range list(v) {
			rows = append(rows, []string{str(i["email"]), str(i["role"]), str(i["status"]), str(i["id"])})
		}
		table(rows)
	case "uninvite":
		if err := p.call("POST", "organization.removeInvitation", obj{"invitationId": arg(1)}, nil); err != nil {
			return err
		}
		done("invitation %s removed", arg(1))
	default:
		return fmt.Errorf("member invite|rm|invitations|uninvite")
	}
	return nil
}

// cmdAPIKey: apikey list | create NAME [--days N] | rm ID
func cmdAPIKey(p Panel, args []string, f F) error {
	arg := func(i int) string {
		if len(args) > i {
			return args[i]
		}
		return ""
	}
	switch arg(0) {
	case "list":
		var me obj
		if err := p.call("GET", "user.get", nil, &me); err != nil {
			return err
		}
		rows := [][]string{{"NAME", "ID", "EXPIRES"}}
		for _, k := range list(me["apiKeys"]) {
			rows = append(rows, []string{str(k["name"]), str(k["id"]), str(k["expiresAt"])})
		}
		table(rows)
	case "create":
		body := obj{"name": arg(1), "metadata": obj{}}
		if d := f.n("days", 0); d > 0 {
			body["expiresIn"] = d * 86400
		}
		var r obj
		if err := p.call("POST", "user.createApiKey", body, &r); err != nil {
			return err
		}
		done("created key %s", str(r["key"]))
	case "rm":
		if err := p.call("POST", "user.deleteApiKey", obj{"apiKeyId": arg(1)}, nil); err != nil {
			return err
		}
		done("api key %s deleted", arg(1))
	default:
		return fmt.Errorf("apikey list|create|rm")
	}
	return nil
}

func cmdOrgs(p Panel) error {
	var v any
	if err := p.call("GET", "organization.all", nil, &v); err != nil {
		return err
	}
	rows := [][]string{{"NAME", "ID"}}
	for _, o := range list(v) {
		rows = append(rows, []string{str(o["name"]), str(o["id"])})
	}
	table(rows)
	return nil
}
