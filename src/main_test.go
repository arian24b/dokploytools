package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMergeEnv(t *testing.T) {
	got := mergeEnv("A=1\nB=2", []string{"B=3", "C=4"}, []string{"A"})
	if got != "B=3\nC=4" {
		t.Fatalf("got %q", got)
	}
}

func TestSeal(t *testing.T) {
	t.Setenv("DOKPLOYTOOLS_PASSPHRASE", "pw")
	s := seal("secret")
	if !strings.HasPrefix(s, "enc:") {
		t.Fatal("not sealed")
	}
	if k, err := unseal(s); err != nil || k != "secret" {
		t.Fatalf("roundtrip %q %v", k, err)
	}
	t.Setenv("DOKPLOYTOOLS_PASSPHRASE", "bad")
	if _, err := unseal(s); err == nil {
		t.Fatal("wrong passphrase accepted")
	}
}

// mock Dokploy: new project with an environment; deploy must create compose, set git source, add domain, deploy.
func TestDeployFlow(t *testing.T) {
	var calls []string
	bodies := map[string]obj{}
	composeMade, projMade := false, false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ep := strings.TrimPrefix(r.URL.Path, "/api/")
		calls = append(calls, ep)
		var b obj
		bb, _ := io.ReadAll(r.Body)
		json.Unmarshal(bb, &b)
		bodies[ep] = b
		if r.Header.Get("x-api-key") != "k" {
			w.WriteHeader(401)
			return
		}
		switch ep {
		case "project.all":
			if !projMade {
				w.Write([]byte(`[]`))
				return
			}
			comp := `[]`
			if composeMade {
				comp = `[{"composeId":"c1","name":"app","composeStatus":"idle"}]`
			}
			w.Write([]byte(`[{"projectId":"p1","name":"app","environments":[{"environmentId":"e1","name":"production","compose":` + comp + `}]}]`))
		case "project.create":
			projMade = true
			w.Write([]byte(`{}`))
		case "compose.create":
			composeMade = true
			w.Write([]byte(`{"composeId":"c1"}`))
		case "compose.one":
			w.Write([]byte(`{"composeId":"c1","env":"","domains":[]}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	p := Panel{"t", srv.URL, "k"}
	_, f := parse([]string{"--repo", "https://github.com/x/app.git", "--set", "A=1", "--domain", "a.example.com", "--service", "web", "--port", "80"})
	jsonOut = true
	defer func() { jsonOut, jsonRows = false, nil }()
	if err := cmdDeploy(p, f); err != nil {
		t.Fatal(err)
	}
	if u := bodies["compose.update"]; u["customGitUrl"] != "https://github.com/x/app.git" || u["env"] != "A=1" || u["sourceType"] != "git" {
		t.Fatalf("compose.update body %v", u)
	}
	if d := bodies["domain.create"]; d["composeId"] != "c1" || d["serviceName"] != "web" || d["certificateType"] != "letsencrypt" {
		t.Fatalf("domain.create body %v", d)
	}
	if bodies["compose.deploy"]["composeId"] != "c1" {
		t.Fatalf("deploy not called: %v", calls)
	}
	if bodies["project.create"]["name"] != "app" {
		t.Fatalf("project name %v", bodies["project.create"])
	}
}

func TestHelpDocs(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range docs {
		if seen[d.name] {
			t.Fatalf("duplicate doc %q", d.name)
		}
		seen[d.name] = true
		if !hasPrefix(strings.Fields(d.name), strings.Fields(d.name)[:1]) {
			t.Fatalf("prefix match broken for %q", d.name)
		}
	}
	if got := colorJSON(obj{"a": []any{1.0, true, nil, "x"}}, 0); !strings.Contains(got, `"a"`) {
		t.Fatalf("colorJSON lost data: %s", got)
	}
}
