package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func latestRelease() (tag string, err error) {
	c := http.Client{Timeout: 10 * time.Second}
	resp, err := c.Get("https://api.github.com/repos/arian24b/dokploytools/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("github: %s", resp.Status)
	}
	var r struct {
		Tag string `json:"tag_name"`
	}
	return r.Tag, json.NewDecoder(resp.Body).Decode(&r)
}

// cmdSelfUpdate upgrades dokploytools itself: through uv/pipx when installed from PyPI, else by replacing the binary.
func cmdSelfUpdate(check bool) {
	latest, err := latestRelease()
	fmt.Printf("installed: %s\nlatest:    %s\n", version, orStr(latest, "unknown (offline or not released)"))
	if err != nil || check {
		return
	}
	if strings.TrimPrefix(latest, "v") == strings.TrimPrefix(version, "v") {
		uiSuccess("already up to date")
		return
	}
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	slash := filepath.ToSlash(exe)
	switch {
	case strings.Contains(slash, "/uv/tools/"):
		runOrDie("uv", "tool", "upgrade", "dokploytools")
	case strings.Contains(slash, "/pipx/"):
		runOrDie("pipx", "upgrade", "dokploytools")
	default:
		replaceBinary(exe, latest)
	}
}

func orStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func runOrDie(name string, args ...string) {
	uiHint("running: %s %s", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		die("%s failed: %v", name, err)
	}
}

func replaceBinary(exe, tag string) {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	url := fmt.Sprintf("https://github.com/arian24b/dokploytools/releases/download/%s/dokploytools-%s-%s%s", tag, runtime.GOOS, runtime.GOARCH, ext)
	uiHint("downloading %s", url)
	resp, err := http.Get(url)
	if err != nil || resp.StatusCode != 200 {
		die("download failed: %v", orErr(err, resp))
	}
	defer resp.Body.Close()
	tmp := exe + ".new"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		dieHint("Re-run with permission to write "+filepath.Dir(exe), "cannot write %s: %v", tmp, err)
	}
	_, err = io.Copy(f, resp.Body)
	f.Close()
	if err == nil {
		err = os.Rename(tmp, exe)
	}
	if err != nil {
		os.Remove(tmp)
		die("update failed: %v", err)
	}
	uiSuccess("updated to %s", tag)
}

func orErr(err error, resp *http.Response) any {
	if err != nil {
		return err
	}
	return resp.Status
}
