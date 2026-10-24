package run

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/ansi"
)

const (
	runFile     = "run.json"
	lockFile    = "lock"
	sessionLock = "review.lock"
	latestFile  = "latest"
)

// lockTimeout is how long a command waits for another to release a run. A variable so tests can shorten it.
var lockTimeout = 3 * time.Second

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,80}$`)

// DataRoot is SIFT_HOME, else $XDG_DATA_HOME/sift, else ~/.local/share/sift.
func DataRoot(getenv func(string) string) (string, error) {
	if v := getenv("SIFT_HOME"); v != "" {
		return filepath.Abs(v)
	}
	if v := getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Abs(filepath.Join(v, "sift"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating the home directory for the data root: %w", err)
	}
	return filepath.Join(home, ".local", "share", "sift"), nil
}

func runsDir(root string) string { return filepath.Join(root, "runs") }

func Dir(root, id string) string { return filepath.Join(runsDir(root), id) }

func checkID(id string) error {
	if !idPattern.MatchString(id) {
		return refuse("unknown-run", "run sift list to see run ids", "%q is not a run id", id)
	}
	return nil
}

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

func newID(name string, now time.Time) string {
	slug := strings.Trim(slugStrip.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	if slug == "" {
		slug = "review"
	}
	return slug + "-" + now.Format("20060102-150405")
}

const (
	maxAgent = 24
	maxFiles = 200
)

// Start describes a new run: the files to review, and the directory their paths are shown relative to.
type Start struct {
	Paths []string
	Cwd   string
	Title string
	// Agent is the name the human knows the agent by, such as Codex; empty means "the agent".
	Agent string
}

// validateAgent keeps the name to one short line of printable text, since the review shows it on screen and in
// messages that do not pass through the interface's own sanitizing.
func validateAgent(agent string) error {
	if hasControl(agent) {
		return refuse("invalid-args", "pass a plain name such as Claude or Codex",
			"agent name contains control or formatting characters")
	}
	if ansi.StringWidth(agent) > maxAgent {
		return refuse("invalid-args", "pass a short name such as Claude or Codex",
			"agent name is wider than %d columns", maxAgent)
	}
	return nil
}

func snapshotPath(dir string, i int) string { return filepath.Join(dir, "files", strconv.Itoa(i)) }

// storageErr turns a failure to write sift's data directory into the refusal a sandboxed agent acts on.
func storageErr(what string, err error) error {
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EROFS) {
		return refuse("data-root", "if a sandbox blocks writing sift's data directory, run sift with your host's "+
			"approval to run outside the sandbox", "%s: %v", what, err)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// realPath resolves symlinks, keeping the path as given when it cannot be resolved.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func displayPath(cwd, abs string) string {
	if rel, err := filepath.Rel(cwd, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel
	}
	return abs
}

// Create snapshots the files into a new run and makes it the latest.
func Create(root string, st Start, now time.Time) (*Run, error) {
	agent := strings.TrimSpace(st.Agent)
	if err := validateAgent(agent); err != nil {
		return nil, err
	}
	if len(st.Paths) == 0 {
		return nil, refuse("invalid-args", "pass the plan, spec or code files to review", "no files to review")
	}
	cwd, err := filepath.Abs(st.Cwd)
	if err != nil {
		return nil, err
	}
	var files []File
	var contents [][]byte
	var seen []os.FileInfo
	for _, p := range st.Paths {
		given := p
		if !filepath.IsAbs(given) {
			given = filepath.Join(cwd, p)
		}
		given = filepath.Clean(given)
		info, err := os.Stat(given)
		if err != nil {
			return nil, refuse("file-unreadable", "pass existing files to review", "reading %s: %v", p, err)
		}
		// File identity, not the name, so plan.md and PLAN.md on a case-insensitive disk, or a symlink and its
		// target, count once.
		if slices.ContainsFunc(seen, func(s os.FileInfo) bool { return os.SameFile(s, info) }) {
			continue
		}
		seen = append(seen, info)
		b, err := os.ReadFile(given)
		if err != nil {
			return nil, refuse("file-unreadable", "pass existing files to review", "reading %s: %v", p, err)
		}
		if !Reviewable(b) {
			return nil, refuse("file-unreadable", "leave out empty and binary files", "%s is empty or binary", p)
		}
		// Shown as the agent named it; matched and edited by its resolved path.
		path := displayPath(cwd, given)
		files = append(files, File{Path: path, Abs: realPath(given), Lines: len(ParseDoc(b, path).Lines)})
		contents = append(contents, b)
	}
	if len(files) > maxFiles {
		return nil, refuse("invalid-args", fmt.Sprintf("review at most %d files in one round", maxFiles),
			"%d files is too many for one review", len(files))
	}
	title := strings.TrimSpace(st.Title)
	if hasControl(title) {
		return nil, refuse("invalid-args", "pass a plain one-line title", "title contains control or formatting characters")
	}
	if title == "" {
		title = filepath.Base(files[0].Path)
		if len(files) > 1 {
			title = fmt.Sprintf("%s and %d more", title, len(files)-1)
		}
	}
	id := newID(title, now)
	dir := Dir(root, id)
	if err := os.MkdirAll(runsDir(root), 0o755); err != nil {
		return nil, storageErr("creating sift's data directory "+root, err)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, refuse("run-exists", "wait a second and run sift start again", "run %s already exists", id)
		}
		return nil, storageErr("creating run directory", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "files"), 0o755); err != nil {
		return nil, storageErr("creating run directory", err)
	}
	for i, b := range contents {
		if err := writeAtomic(snapshotPath(dir, i), b); err != nil {
			return nil, err
		}
	}
	r := &Run{Version: SchemaVersion, ID: id, Title: title, Agent: agent, Root: cwd, Files: files, CreatedAt: now,
		Findings: []Finding{}}
	if err := save(dir, r); err != nil {
		return nil, err
	}
	if err := writeAtomic(filepath.Join(root, latestFile), []byte(id+"\n")); err != nil {
		return nil, err
	}
	return r, nil
}

// Resolve picks the run a command acts on: the --run flag, else SIFT_RUN, else the latest run started.
func Resolve(root, flagRun string, getenv func(string) string) (string, error) {
	id := flagRun
	if id == "" {
		id = getenv("SIFT_RUN")
	}
	if id == "" {
		b, err := os.ReadFile(filepath.Join(root, latestFile))
		if errors.Is(err, os.ErrNotExist) {
			return "", refuse("no-run", "start one with sift start <files>", "no run has been started")
		}
		if err != nil {
			return "", fmt.Errorf("reading latest run: %w", err)
		}
		id = strings.TrimSpace(string(b))
	}
	if err := checkID(id); err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(Dir(root, id), runFile)); err != nil {
		return "", refuse("unknown-run", "run sift list to see run ids", "no run %s", id)
	}
	return id, nil
}

// LoadRun reads the run without its file snapshots, which never change after start, for callers that poll it.
func LoadRun(root, id string) (*Run, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(Dir(root, id), runFile))
	if err != nil {
		return nil, fmt.Errorf("reading run %s: %w", id, err)
	}
	var r Run
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("parsing run %s: %w", id, err)
	}
	if r.Version != SchemaVersion {
		return nil, fmt.Errorf("run %s has schema version %d, this build reads %d", id, r.Version, SchemaVersion)
	}
	return &r, nil
}

func Load(root, id string) (*Run, Docs, error) {
	r, err := LoadRun(root, id)
	if err != nil {
		return nil, nil, err
	}
	dir := Dir(root, id)
	docs := make(Docs, len(r.Files))
	for i, f := range r.Files {
		b, err := os.ReadFile(snapshotPath(dir, i))
		if err != nil {
			return nil, nil, fmt.Errorf("reading snapshot of %s in run %s: %w", f.Path, id, err)
		}
		docs[f.Path] = ParseDoc(b, f.Path)
	}
	return r, docs, nil
}

// Update applies fn under the run's lock and saves the result. fn sees the run as it is on disk, so concurrent
// writers (the agent and the review interface) never lose each other's changes.
func Update(root, id string, fn func(*Run, Docs) error) (*Run, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	dir := Dir(root, id)
	unlock, err := lock(dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	r, docs, err := Load(root, id)
	if err != nil {
		return nil, err
	}
	if err := fn(r, docs); err != nil {
		return nil, err
	}
	if err := save(dir, r); err != nil {
		return nil, err
	}
	return r, nil
}

func lock(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, storageErr("opening run lock", err)
	}
	deadline := time.Now().Add(lockTimeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("locking run: %w", err)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, refuse("locked", "retry the command", "run is locked by another sift process")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func save(dir string, r *Run) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding run: %w", err)
	}
	return writeAtomic(filepath.Join(dir, runFile), append(b, '\n'))
}

func writeAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return storageErr("writing "+path, err)
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return storageErr("writing "+path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return storageErr("writing "+path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return storageErr("writing "+path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return storageErr("writing "+path, err)
	}
	return nil
}

// Skipped is a run directory List could not read, such as one left behind by an interrupted start.
type Skipped struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// List returns every readable run, newest first, and the run directories it could not read.
func List(root string) ([]*Run, []Skipped, error) {
	entries, err := os.ReadDir(runsDir(root))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("listing runs: %w", err)
	}
	var runs []*Run
	var skipped []Skipped
	for _, e := range entries {
		if !e.IsDir() || checkID(e.Name()) != nil {
			continue
		}
		r, err := LoadRun(root, e.Name())
		if err != nil {
			skipped = append(skipped, Skipped{ID: e.Name(), Reason: err.Error()})
			continue
		}
		runs = append(runs, r)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.After(runs[j].CreatedAt) })
	return runs, skipped, nil
}

// The review interface holds an exclusive flock on review.lock for its lifetime. The kernel drops it when the
// process dies, however it dies, so a crashed or closed pane never leaves the run looking open.

// OpenSession reports whether a review interface is running for the run.
func OpenSession(root, id string) (bool, error) {
	f, err := os.OpenFile(filepath.Join(Dir(root, id), sessionLock), os.O_CREATE|os.O_RDONLY, 0o644)
	if err != nil {
		return false, storageErr("opening review lock", err)
	}
	// Opened only to probe the lock, so closing it cannot lose anything.
	defer func() { _ = f.Close() }()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return false, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return true, nil
	}
	return false, fmt.Errorf("probing review lock: %w", err)
}

// MarkSession claims the run's review for this process and returns the function that releases it.
func MarkSession(root, id string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(Dir(root, id), sessionLock), os.O_CREATE|os.O_RDONLY, 0o644)
	if err != nil {
		return nil, storageErr("opening review lock", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, refuse("review-open", "switch to the pane or terminal already reviewing this run",
				"sift review is already open for %s", id)
		}
		return nil, fmt.Errorf("locking review: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
