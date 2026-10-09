package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/crypto/scrypt"
)

type Panel struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	APIKey string `json:"apiKey"`
}

type any = interface{}
type obj = map[string]any

var (
	cfgPath  = func() string { d, _ := os.UserConfigDir(); return filepath.Join(d, "dokploytools", "panels.json") }()
	jsonOut  bool
	curPanel string
	jsonRows []obj
	failed   bool
)

func die(f string, a ...any) { fmt.Fprintf(os.Stderr, "error: "+f+"\n", a...); os.Exit(1) }

// ---- config: file (optionally AES-GCM sealed) + DOKPLOY_<NAME>_URL/_KEY env panels ----

func seal(key string) string {
	pw := os.Getenv("DOKPLOYTOOLS_PASSPHRASE")
	if pw == "" || strings.HasPrefix(key, "enc:") {
		return key
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	k, _ := scrypt.Key([]byte(pw), salt, 1<<15, 8, 1, 32)
	blk, _ := aes.NewCipher(k)
	g, _ := cipher.NewGCM(blk)
	nonce := make([]byte, g.NonceSize())
	rand.Read(nonce)
	raw := append(append(append([]byte{}, salt...), nonce...), g.Seal(nil, nonce, []byte(key), nil)...)
	return "enc:" + base64.StdEncoding.EncodeToString(raw)
}

func unseal(s string) (string, error) {
	if !strings.HasPrefix(s, "enc:") {
		return s, nil
	}
	pw := os.Getenv("DOKPLOYTOOLS_PASSPHRASE")
	if pw == "" {
		return "", fmt.Errorf("config is encrypted; set DOKPLOYTOOLS_PASSPHRASE")
	}
	raw, err := base64.StdEncoding.DecodeString(s[4:])
	if err != nil || len(raw) < 28 {
		return "", fmt.Errorf("bad encrypted key")
	}
	k, _ := scrypt.Key([]byte(pw), raw[:16], 1<<15, 8, 1, 32)
	blk, _ := aes.NewCipher(k)
	g, _ := cipher.NewGCM(blk)
	n := g.NonceSize()
	pt, err := g.Open(nil, raw[16:16+n], raw[16+n:], nil)
	if err != nil {
		return "", fmt.Errorf("wrong passphrase")
	}
	return string(pt), nil
}

func loadFile() []Panel {
	b, err := os.ReadFile(cfgPath)
	var ps []Panel
	if err == nil {
		json.Unmarshal(b, &ps)
	}
	for i := range ps {
		k, err := unseal(ps[i].APIKey)
		if err != nil {
			die("%v", err)
		}
		ps[i].APIKey = k
	}
	return ps
}

func save(ps []Panel) {
	os.MkdirAll(filepath.Dir(cfgPath), 0o700)
	out := make([]Panel, len(ps))
	for i, p := range ps {
		p.APIKey = seal(p.APIKey)
		out[i] = p
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
		die("%v", err)
	}
}

// load = file panels + env panels (env wins, never written back).
func load() []Panel {
	ps := loadFile()
	for _, e := range os.Environ() {
		k, v, _ := strings.Cut(e, "=")
		if !strings.HasPrefix(k, "DOKPLOY_") || !strings.HasSuffix(k, "_KEY") || len(k) <= 12 {
			continue
		}
		raw := k[8 : len(k)-4]
		u := os.Getenv("DOKPLOY_" + raw + "_URL")
		if u == "" {
			continue
		}
		name := strings.ToLower(strings.ReplaceAll(raw, "_", "-"))
		p := Panel{name, u, v}
		replaced := false
		for i := range ps {
			if ps[i].Name == name {
				ps[i], replaced = p, true
			}
		}
		if !replaced {
			ps = append(ps, p)
		}
	}
	return ps
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

// ---- http ----

var hc = &http.Client{Timeout: 60 * time.Second}

// call hits /api/<path>. GET sends body (an obj) as query string; POST sends it as JSON.
func (p Panel) call(method, path string, body any, out any) error {
	u := strings.TrimRight(p.URL, "/") + "/api/" + path
	var r io.Reader
	if method == "GET" {
		if q, ok := body.(obj); ok && len(q) > 0 {
			v := url.Values{}
			for k, x := range q {
				if x != nil {
					v.Set(k, fmt.Sprint(x))
				}
			}
			u += "?" + v.Encode()
		}
	} else {
		if body == nil {
			body = obj{}
		}
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, u, r)
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
	if out != nil && len(bytes.TrimSpace(b)) > 0 {
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

// ---- value helpers (API shapes vary by Dokploy version, so we stay dynamic) ----

func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(x), "%"), 64)
		return f
	}
	return 0
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

func sub(o obj, keys ...string) any {
	var cur any = o
	for _, k := range keys {
		m, ok := cur.(obj)
		if !ok {
			return nil
		}
		cur = m[k]
	}
	return cur
}

func compact(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func newestFirst(xs []obj, key string) {
	sort.SliceStable(xs, func(i, j int) bool { return str(xs[i][key]) > str(xs[j][key]) })
}

// ---- output: human tables, or JSON rows with --json ----

func table(rows [][]string) {
	if jsonOut {
		for _, r := range rows[1:] {
			m := obj{}
			if curPanel != "" {
				m["panel"] = curPanel
			}
			for i, c := range r {
				m[strings.ToLower(rows[0][i])] = c
			}
			jsonRows = append(jsonRows, m)
		}
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	for _, r := range rows {
		fmt.Fprintln(w, strings.Join(r, "\t"))
	}
	w.Flush()
}

// done reports a one-line result (JSON: {"panel":..,"result":..}).
func done(f string, a ...any) {
	msg := fmt.Sprintf(f, a...)
	if jsonOut {
		jsonRows = append(jsonRows, obj{"panel": curPanel, "result": msg})
		return
	}
	fmt.Println("  " + msg)
}

func flush() {
	if !jsonOut {
		return
	}
	if jsonRows == nil {
		jsonRows = []obj{}
	}
	b, _ := json.MarshalIndent(jsonRows, "", "  ")
	fmt.Println(string(b))
	jsonRows = nil
}

// each runs fn for every selected panel, isolating errors per panel.
func each(name string, fn func(Panel) error) {
	for _, p := range pick(name) {
		curPanel = p.Name
		if !jsonOut {
			fmt.Printf("\n== %s (%s)\n", p.Name, p.URL)
		}
		if err := fn(p); err != nil {
			failed = true
			if jsonOut {
				jsonRows = append(jsonRows, obj{"panel": p.Name, "error": err.Error()})
			} else {
				fmt.Println("  error:", err)
			}
		}
	}
	curPanel = ""
}

// ---- flags ----

type F struct{ m map[string][]string }

var boolFlags = map[string]bool{"json": true, "check": true, "stats": true, "no-https": true, "no-deploy": true,
	"wait": true, "auto-renew": true, "disabled": true, "auto-deploy": true, "no-certs": true}

func parse(args []string) (pos []string, f F) {
	f.m = map[string][]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			pos = append(pos, a)
			continue
		}
		k, v, eq := strings.Cut(a[2:], "=")
		if !eq {
			v = "true"
			if !boolFlags[k] && i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				v = args[i+1]
				i++
			}
		}
		f.m[k] = append(f.m[k], v)
	}
	return
}

func (f F) s(k string) string {
	if v := f.m[k]; len(v) > 0 {
		return v[len(v)-1]
	}
	return ""
}
func (f F) all(k string) []string { return f.m[k] }
func (f F) has(k string) bool     { return len(f.m[k]) > 0 }
func (f F) or(k, def string) string {
	if v := f.s(k); v != "" {
		return v
	}
	return def
}
func (f F) n(k string, def int) int {
	if v, err := strconv.Atoi(f.s(k)); err == nil {
		return v
	}
	return def
}
