package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseInterspersedAndTerminator(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	title := fs.String("title", "", "")
	pos, err := parse(fs, []string{"plan.md", "--title", "x", "--", "-a", "--title"})
	if err != nil {
		t.Fatal(err)
	}
	if *title != "x" || !slices.Equal(pos, []string{"plan.md", "-a", "--title"}) {
		t.Fatalf("title=%q pos=%v", *title, pos)
	}
}

func TestStartChangedReviewsGitChanges(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write("kept.go", "package a\n")
	write("edited.go", "package a\n")
	write("gone.go", "package a\n")
	git("add", ".")
	git("commit", "-q", "-m", "init")
	write("edited.go", "package a\n\nfunc F() {}\n")
	write("sub/new.md", "# New\n")
	write("logo.bin", "a\x00b")
	if err := os.Remove(filepath.Join(dir, "gone.go")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(dir, "sub"))

	var out bytes.Buffer
	home := t.TempDir()
	env := &Env{Args: []string{"start", "--changed", "--agent", "Codex"}, Stdin: strings.NewReader(""), Stdout: &out,
		Stderr: io.Discard, Getenv: func(k string) string {
			if k == "SIFT_HOME" {
				return home
			}
			return ""
		}, Now: time.Now}
	if code := Main(env); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	var res struct {
		Files   []struct{ Path string }
		Skipped []struct{ Path, Reason string }
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range res.Files {
		paths = append(paths, f.Path)
	}
	slices.Sort(paths)
	// Run from a sub directory, paths still show from the repository root, as git names them.
	if want := []string{"edited.go", "sub/new.md"}; !slices.Equal(paths, want) {
		t.Fatalf("files = %v, want %v", paths, want)
	}
	want := []struct{ Path, Reason string }{{"gone.go", "deleted"}, {"logo.bin", "empty or binary"}}
	if !slices.Equal(res.Skipped, want) {
		t.Fatalf("skipped = %+v, want %+v", res.Skipped, want)
	}
}

func TestStartChangedWorksBeforeTheFirstCommit(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for _, name := range []string{"a.go", "b.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "-C", dir, "add", "a.go").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	t.Chdir(dir)
	var out bytes.Buffer
	home := t.TempDir()
	env := &Env{Args: []string{"start", "--changed"}, Stdin: strings.NewReader(""), Stdout: &out, Stderr: io.Discard,
		Getenv: func(k string) string {
			if k == "SIFT_HOME" {
				return home
			}
			return ""
		}, Now: time.Now}
	if code := Main(env); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	var res struct{ Files []struct{ Path string } }
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 2 {
		t.Fatalf("files = %+v, want the staged a.go and the untracked b.md", res.Files)
	}
}

func runSift(t *testing.T, home string, args ...string) (int, map[string]any) {
	t.Helper()
	var out bytes.Buffer
	env := &Env{Args: args, Stdin: strings.NewReader(""), Stdout: &out, Stderr: io.Discard,
		Getenv: func(k string) string {
			if k == "SIFT_HOME" {
				return home
			}
			return ""
		}, Now: time.Now}
	code := Main(env)
	var res map[string]any
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out.String())
	}
	return code, res
}

func TestStartChangedUsesTheMergeBase(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	write("base.go", "package a\n")
	git("add", ".")
	git("commit", "-q", "-m", "base")
	git("checkout", "-q", "-b", "feature")
	write("feature.go", "package a\n")
	git("add", ".")
	git("commit", "-q", "-m", "feature")
	git("checkout", "-q", "main")
	write("upstream.go", "package a\n")
	git("add", ".")
	git("commit", "-q", "-m", "upstream")
	git("checkout", "-q", "feature")
	write("wip.go", "package a\n")
	git("add", "wip.go")
	git("config", "diff.relative", "true")
	t.Chdir(dir)

	code, res := runSift(t, t.TempDir(), "start", "--changed", "--base", "main")
	if code != 0 {
		t.Fatalf("exit %d: %v", code, res)
	}
	var paths []string
	for _, f := range res["files"].([]any) {
		paths = append(paths, f.(map[string]any)["path"].(string))
	}
	slices.Sort(paths)
	// The branch's own commit and the staged file; not upstream.go, which only main changed.
	if want := []string{"feature.go", "wip.go"}; !slices.Equal(paths, want) {
		t.Fatalf("files = %v, want %v", paths, want)
	}
	for _, base := range []string{"--cached", "main..feature", "--output=x"} {
		if code, res := runSift(t, t.TempDir(), "start", "--changed", "--base="+base); code == 0 {
			t.Errorf("--base %s was accepted: %v", base, res)
		}
	}
}

func TestAddResolvesPathsFromTheAgentsDirectory(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"README.md", "svc/README.md", "svc/only.go"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte("one\ntwo\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	home := t.TempDir()
	t.Chdir(dir)
	if code, res := runSift(t, home, "start", "README.md", "svc/README.md", "svc/only.go"); code != 0 {
		t.Fatalf("start: %v", res)
	}
	t.Chdir(filepath.Join(dir, "svc"))
	add := func(anchor string) map[string]any {
		t.Helper()
		f := filepath.Join(t.TempDir(), "f.json")
		if err := os.WriteFile(f, []byte(`{"title":"t","body":"b","anchor":`+anchor+`}`), 0o644); err != nil {
			t.Fatal(err)
		}
		_, res := runSift(t, home, "add", "--from", f)
		return res
	}
	if res := add(`{"path":"README.md","startLine":1}`); res["ok"] != false ||
		!strings.Contains(res["error"].(map[string]any)["message"].(string), "could mean") {
		t.Fatalf("README.md from svc/ is ambiguous, got %v", res)
	}
	if res := add(`{"path":"only.go","startLine":1}`); res["ok"] != true {
		t.Fatalf("only.go relative to the agent's directory should resolve: %v", res)
	}
	if res := add(`{"path":"svc/README.md","startLine":1}`); res["ok"] != true {
		t.Fatalf("the path as start printed it should resolve: %v", res)
	}
}

func TestDataRootRefusalOnEveryWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("# p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	home := t.TempDir()
	if code, res := runSift(t, home, "start", "plan.md"); code != 0 {
		t.Fatalf("start: %v", res)
	}
	if err := os.Chmod(filepath.Join(home, "runs"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(home, "runs"), 0o755) })
	_, res := runSift(t, home, "start", "plan.md", "--title", "second")
	if code := res["error"].(map[string]any)["code"]; code != "data-root" {
		t.Fatalf("code = %v, want data-root once runs/ exists but cannot be written", code)
	}
}

func TestShowStripsControlCharacters(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("# p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if code, res := runSift(t, t.TempDir(), "start", "plan.md", "--title", "T\x1b]0;pwned\x07"); code == 0 {
		t.Fatalf("a title with escape codes was accepted: %v", res)
	}
	if got := printable("a\x1b[31mb\u202ec\nd"); got != "a[31mbc\nd" {
		t.Fatalf("printable = %q", got)
	}
}

func TestVersionMatchesEveryManifest(t *testing.T) {
	code, out := 0, bytes.Buffer{}
	code = Main(&Env{Args: []string{"--version"}, Stdout: &out, Stderr: io.Discard, Getenv: func(string) string { return "" }})
	if code != 0 || out.String() != "sift "+Version+"\n" {
		t.Fatalf("exit %d, output %q", code, out.String())
	}
	for _, p := range []string{".claude-plugin/plugin.json", ".codex-plugin/plugin.json", ".cursor-plugin/plugin.json",
		"gemini-extension.json", "package.json"} {
		b, err := os.ReadFile(filepath.Join("..", "..", p))
		if err != nil {
			t.Fatal(err)
		}
		var m struct{ Version string }
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if m.Version != Version {
			t.Errorf("%s has version %q, the CLI is %q", p, m.Version, Version)
		}
	}
}
