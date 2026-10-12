package main

// cf-style terminal UI: semantic palette, status lines, boxed errors, JSON colors, prompts.
// Same roles and shapes as the Python `cfui` package so every tool looks and behaves alike.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

var palette = map[string][3]int{
	"brand": {0xBF, 0x66, 0x00}, "success": {0x3A, 0x8E, 0x52}, "error": {0xD4, 0x53, 0x50},
	"warning": {0xBF, 0x66, 0x00}, "info": {0x3D, 0x82, 0xBB}, "muted": {0x7F, 0x7F, 0x7F}, "boolean": {0x92, 0x6C, 0xC1},
}
var basic = map[string]int{"brand": 33, "success": 32, "error": 31, "warning": 33, "info": 34, "muted": 90, "boolean": 35}
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)
var quiet bool // -q / DOKPLOYTOOLS_QUIET

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func colorOn(f *os.File) bool {
	_, no := os.LookupEnv("NO_COLOR")
	return isTTY(f) && !no && os.Getenv("FORCE_COLOR") != "0"
}

func paint(role, s string, f *os.File) string {
	if !colorOn(f) {
		return s
	}
	c, code := palette[role], ""
	switch ct := os.Getenv("COLORTERM"); {
	case ct == "truecolor" || ct == "24bit":
		code = fmt.Sprintf("38;2;%d;%d;%d", c[0], c[1], c[2])
	case strings.Contains(os.Getenv("TERM"), "256"):
		r := func(v int) int { return (v*5 + 127) / 255 }
		code = fmt.Sprintf("38;5;%d", 16+36*r(c[0])+6*r(c[1])+r(c[2]))
	default:
		code = strconv.Itoa(basic[role])
	}
	return "\x1b[" + code + "m" + s + "\x1b[39m"
}

func bold(s string, f *os.File) string {
	if colorOn(f) {
		return "\x1b[1m" + s + "\x1b[22m"
	}
	return s
}

func sym(name string) string {
	uni := map[string]string{"ok": "✔", "err": "✖", "warn": "⚠", "info": "ℹ", "arrow": "→"}
	asc := map[string]string{"ok": "√", "err": "×", "warn": "‼", "info": "i", "arrow": ">"}
	if runtime.GOOS == "windows" && os.Getenv("WT_SESSION") == "" {
		return asc[name]
	}
	return uni[name]
}

func marker(role, symbol, msg string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", paint(role, sym(symbol), os.Stderr), msg)
}

// success markers carry no payload: stderr, and only on a TTY.
func uiSuccess(f string, a ...any) {
	if !quiet && isTTY(os.Stderr) {
		marker("success", "ok", fmt.Sprintf(f, a...))
	}
}
func uiWarn(f string, a ...any) {
	if !quiet {
		marker("warning", "warn", fmt.Sprintf(f, a...))
	}
}
func uiInfo(f string, a ...any) {
	if !quiet {
		marker("info", "info", fmt.Sprintf(f, a...))
	}
}
func uiHint(f string, a ...any) {
	if !quiet {
		fmt.Fprintln(os.Stderr, paint("muted", sym("arrow")+" "+fmt.Sprintf(f, a...), os.Stderr))
	}
}

// banner: two lines on stderr, interactive only.
func banner() {
	if quiet || !isTTY(os.Stderr) {
		return
	}
	e := os.Stderr
	line := paint("brand", bold("dokploytools", e), e) + " " + paint("muted", "·", e) + " " + paint("muted", version, e)
	fmt.Fprintln(e, line)
	fmt.Fprintln(e, paint("brand", strings.Repeat("─", len([]rune(ansiRe.ReplaceAllString(line, "")))), e))
}

func wrap(s string, w int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			if line != "" && len(line)+1+len(word) > w {
				out = append(out, line)
				line = ""
			}
			if line != "" {
				line += " "
			}
			line += word
		}
		out = append(out, line)
	}
	return out
}

func errorBox(msg, hint string) {
	e := os.Stderr
	var b strings.Builder
	b.WriteString("\n" + paint("error", "┌", e) + " " + paint("error", bold("Error", e), e) + "\n")
	for _, l := range wrap(msg, 78) {
		b.WriteString(paint("error", "│", e) + " " + paint("error", bold(l, e), e) + "\n")
	}
	if hint != "" {
		b.WriteString(paint("error", "│", e) + "\n")
		for _, l := range strings.Split(hint, "\n") {
			b.WriteString(paint("error", "│", e) + " " + paint("muted", l, e) + "\n")
		}
	}
	b.WriteString(paint("error", "└", e) + "\n")
	fmt.Fprint(e, b.String())
}

func die(f string, a ...any) { errorBox(fmt.Sprintf(f, a...), ""); os.Exit(1) }
func dieHint(hint, f string, a ...any) {
	errorBox(fmt.Sprintf(f, a...), hint)
	os.Exit(1)
}

// colorJSON renders decoded JSON with the palette (keys info, strings success, numbers warning, bools purple, null muted).
func colorJSON(v any, ind int) string {
	pad := strings.Repeat("  ", ind)
	o := os.Stdout
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 {
			return "{}"
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			kb, _ := json.Marshal(k)
			parts[i] = pad + "  " + paint("info", string(kb), o) + ": " + colorJSON(t[k], ind+1)
		}
		return "{\n" + strings.Join(parts, ",\n") + "\n" + pad + "}"
	case []any:
		if len(t) == 0 {
			return "[]"
		}
		parts := make([]string, len(t))
		for i, x := range t {
			parts[i] = pad + "  " + colorJSON(x, ind+1)
		}
		return "[\n" + strings.Join(parts, ",\n") + "\n" + pad + "]"
	case bool:
		return paint("boolean", strconv.FormatBool(t), o)
	case nil:
		return paint("muted", "null", o)
	case float64:
		return paint("warning", strconv.FormatFloat(t, 'f', -1, 64), o)
	default:
		b, _ := json.Marshal(t)
		return paint("success", string(b), o)
	}
}

// printJSON: pretty JSON on stdout, colored only on a TTY and under 50k chars.
func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	if colorOn(os.Stdout) && len(b) <= 50000 {
		var back any
		if json.Unmarshal(b, &back) == nil {
			fmt.Println(colorJSON(back, 0))
			return
		}
	}
	fmt.Println(string(b))
}

// ---- prompts (stdlib only, on stderr) ----

var stdin = bufio.NewReader(os.Stdin)

func interactive() bool { return isTTY(os.Stdin) && isTTY(os.Stderr) && os.Getenv("CI") == "" }

func ask(label string, secret bool) string {
	e := os.Stderr
	for {
		fmt.Fprintf(e, "%s %s: ", paint("info", "?", e), label)
		if secret {
			exec.Command("stty", "-echo").Run() // best effort; Windows keeps echo
		}
		line, err := stdin.ReadString('\n')
		if secret {
			exec.Command("stty", "echo").Run()
			fmt.Fprintln(e)
		}
		if err != nil && line == "" {
			fmt.Fprintln(e)
			os.Exit(130) // Ctrl-C / EOF in a prompt
		}
		if v := strings.TrimSpace(line); v != "" {
			return v
		}
		uiWarn("a value is required")
	}
}

// need returns val, or asks for it on a TTY, or fails with the standard non-interactive error.
func need(val, flag, label string, secret bool) string {
	if val != "" {
		return val
	}
	if !interactive() {
		dieHint("", "%s is required. Pass %s <value> or run interactively.", flag, flag)
	}
	uiWarn("%s is required", flag)
	return ask(label, secret)
}
