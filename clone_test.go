package bb

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// cloneEnv sets up a bare repo with one commit, a fake Bitbucket API serving it as repository
// "foo", and a config in an isolated XDG dir. It returns the config (already saved), and a
// counter of API requests.
func cloneEnv(t *testing.T, home string) (*Config, *int) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	bare := filepath.Join(root, "foo.git")
	work := filepath.Join(root, "work")
	for _, d := range []string{bare, work} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git(t, bare, "init", "--bare", "-b", "main")
	git(t, work, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(work, "README"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "init")
	git(t, work, "push", bare, "main")

	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/repositories/ws/foo" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// Both protocols point at the local bare repo, spelled differently like real ssh/https links.
		fmt.Fprintf(w, `{"slug":"foo","links":{"clone":[{"name":"https","href":%q},{"name":"ssh","href":%q}]}}`, "file://"+bare, bare)
	}))
	t.Cleanup(srv.Close)

	old := bitbucketAPI
	bitbucketAPI = srv.URL
	t.Cleanup(func() { bitbucketAPI = old })

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := &Config{Workspace: "ws", Email: "e", Token: "t", Home: home}
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	return cfg, &requests
}

func runBB(args ...string) error {
	_, err := runBBOut(args...)
	return err
}

// runBBOut runs bb and returns what it wrote to stdout via the app (not git's own output).
func runBBOut(args ...string) (string, error) {
	var buf bytes.Buffer
	app := NewApp()
	app.Writer = &buf
	err := app.Run(context.Background(), append([]string{"bb"}, args...))
	return buf.String(), err
}

func assertCloned(t *testing.T, dir string) {
	t.Helper()
	if data, err := os.ReadFile(filepath.Join(dir, "README")); err != nil || string(data) != "hi" {
		t.Errorf("expected a clone in %s: %q, %v", dir, data, err)
	}
}

func TestCloneDefaultsToHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "code", "nested") // doesn't exist yet
	cloneEnv(t, home)

	if err := runBB("clone", "foo"); err != nil {
		t.Fatal(err)
	}
	assertCloned(t, filepath.Join(home, "foo"))
}

func TestCloneHTTPSFlag(t *testing.T) {
	home := t.TempDir()
	cloneEnv(t, home)

	if err := runBB("clone", "--https", "foo"); err != nil {
		t.Fatal(err)
	}
	assertCloned(t, filepath.Join(home, "foo"))
}

func TestCloneToCurrentDirectory(t *testing.T) {
	cloneEnv(t, t.TempDir())
	cwd := t.TempDir()
	t.Chdir(cwd)

	if err := runBB("clone", "foo", "."); err != nil {
		t.Fatal(err)
	}
	assertCloned(t, cwd)
}

func TestCloneToRelativeDirectory(t *testing.T) {
	home := t.TempDir()
	cloneEnv(t, home)
	cwd := t.TempDir()
	t.Chdir(cwd)

	if err := runBB("clone", "foo", "foo"); err != nil {
		t.Fatal(err)
	}
	assertCloned(t, filepath.Join(cwd, "foo"))
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("explicit destination must not touch the home dir, found %v", entries)
	}
}

func TestCloneWithoutHomeFailsWithHint(t *testing.T) {
	_, requests := cloneEnv(t, "")
	cwd := t.TempDir()
	t.Chdir(cwd)

	err := runBB("clone", "foo")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"home directory is not configured", "bb configure", "bb clone foo ."} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
	if *requests != 0 {
		t.Errorf("should fail before calling the API, made %d requests", *requests)
	}
	if entries, _ := os.ReadDir(cwd); len(entries) != 0 {
		t.Errorf("nothing should be cloned, found %v", entries)
	}

	// An explicit destination still works without a home directory.
	if err := runBB("clone", "foo", "."); err != nil {
		t.Fatal(err)
	}
	assertCloned(t, cwd)
}

func TestCloneErrors(t *testing.T) {
	cloneEnv(t, t.TempDir())

	if err := runBB("clone", "nope"); err == nil || !strings.Contains(err.Error(), `repository "nope" not found`) {
		t.Errorf("unknown repo: %v", err)
	}
	if err := runBB("clone"); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Errorf("no args: %v", err)
	}
	if err := runBB("clone", "a", "b", "c"); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Errorf("too many args: %v", err)
	}
}

func TestCloneURL(t *testing.T) {
	var r Repository
	r.Links.Clone = append(r.Links.Clone,
		struct {
			Name string `json:"name"`
			Href string `json:"href"`
		}{"https", "https://x"},
		struct {
			Name string `json:"name"`
			Href string `json:"href"`
		}{"ssh", "git@x"})

	if got, err := r.cloneURL("ssh"); err != nil || got != "git@x" {
		t.Errorf("ssh = %q, %v", got, err)
	}
	if got, err := r.cloneURL("https"); err != nil || got != "https://x" {
		t.Errorf("https = %q, %v", got, err)
	}
	if _, err := (&Repository{Slug: "foo"}).cloneURL("ssh"); err == nil {
		t.Error("expected an error when the repository has no clone links")
	}
}

func TestNormalizeHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"~", home},
		{"~/code", filepath.Join(home, "code")},
		{"/abs/path/", "/abs/path"},
	}
	for _, tc := range tests {
		if got, err := normalizeHome(tc.in); err != nil || got != tc.want {
			t.Errorf("normalize(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}

	if got, err := normalizeHome("rel/dir"); err != nil || !filepath.IsAbs(got) {
		t.Errorf("relative path should become absolute: %q, %v", got, err)
	}
	if _, err := normalizeHome(file); err == nil {
		t.Error("a path that is a file should be rejected")
	}
}

func TestCloneAlreadyCheckedOut(t *testing.T) {
	home := t.TempDir()
	cloneEnv(t, home)
	repoDir := filepath.Join(home, "foo")

	if out, err := runBBOut("clone", "foo"); err != nil || !strings.Contains(out, "Cloned foo to "+repoDir) {
		t.Fatalf("first clone: %q, %v", out, err)
	}

	// Leave a marker to prove the second run doesn't touch the checkout.
	marker := filepath.Join(repoDir, "LOCAL")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runBBOut("clone", "foo")
	if err != nil {
		t.Fatalf("second clone should succeed: %v", err)
	}
	if want := "foo is already checked out at " + repoDir; !strings.Contains(out, want) {
		t.Errorf("output %q should contain %q", out, want)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("existing checkout was modified: %v", err)
	}
}

func TestCloneAlreadyCheckedOutOverOtherProtocol(t *testing.T) {
	home := t.TempDir()
	cloneEnv(t, home)

	// Clone over "https" (a file:// URL here), then ask again over the default "ssh" (plain path).
	if err := runBB("clone", "--https", "foo"); err != nil {
		t.Fatal(err)
	}
	out, err := runBBOut("clone", "foo")
	if err != nil || !strings.Contains(out, "already checked out at "+filepath.Join(home, "foo")) {
		t.Errorf("want already-checked-out, got %q, %v", out, err)
	}
}

func TestCloneAlreadyCheckedOutInCurrentDirectory(t *testing.T) {
	cloneEnv(t, t.TempDir())
	cwd := t.TempDir()
	t.Chdir(cwd)

	if err := runBB("clone", "foo", "."); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(cwd)
	out, err := runBBOut("clone", "foo", ".")
	if err != nil || !strings.Contains(out, "already checked out at ") || !strings.Contains(out, filepath.Base(resolved)) {
		t.Errorf("got %q, %v", out, err)
	}
}

func TestCloneIntoRepoWithDifferentRemoteFails(t *testing.T) {
	home := t.TempDir()
	cloneEnv(t, home)

	other := filepath.Join(home, "foo")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, other, "init", "-b", "main")
	git(t, other, "remote", "add", "origin", "https://bitbucket.org/ws/something-else.git")

	out, err := runBBOut("clone", "foo")
	if err == nil {
		t.Fatalf("expected failure, output %q", out)
	}
	if strings.Contains(out, "already checked out") {
		t.Errorf("a repo with a different remote must not count as a checkout: %q", out)
	}
}

func TestCloneIntoPlainDirNestedInCheckoutStillClones(t *testing.T) {
	home := t.TempDir()
	cloneEnv(t, home)
	if err := runBB("clone", "foo"); err != nil {
		t.Fatal(err)
	}

	// An empty directory inside the checkout is not itself a checkout, despite git reporting the
	// parent's remotes for it.
	nested := filepath.Join(home, "foo", "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := runBBOut("clone", "foo", nested)
	if err != nil || strings.Contains(out, "already checked out") {
		t.Fatalf("got %q, %v", out, err)
	}
	assertCloned(t, nested)
}

func TestNormalizeGitURL(t *testing.T) {
	same := [][]string{
		{"git@bitbucket.org:ws/foo.git", "https://user@bitbucket.org/ws/foo.git", "ssh://git@bitbucket.org/ws/foo", "https://bitbucket.org/ws/foo/", "HTTPS://BitBucket.org/WS/Foo.git"},
		{"/tmp/x/foo.git", "file:///tmp/x/foo.git", "/tmp/x/foo"},
	}
	for _, group := range same {
		want := normalizeGitURL(group[0])
		for _, u := range group[1:] {
			if got := normalizeGitURL(u); got != want {
				t.Errorf("normalize(%q) = %q, want %q (same as %q)", u, got, want, group[0])
			}
		}
	}
	if normalizeGitURL("git@bitbucket.org:ws/foo.git") == normalizeGitURL("git@bitbucket.org:ws/bar.git") {
		t.Error("different repositories must not match")
	}
	if normalizeGitURL("git@bitbucket.org:ws/foo.git") == normalizeGitURL("git@github.com:ws/foo.git") {
		t.Error("different hosts must not match")
	}
}
