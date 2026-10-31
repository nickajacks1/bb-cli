package bb

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// prCreateEnv fakes ws/foo (main branch "main") and records the JSON of each pull request
// created. If reject is non-empty, creation fails with that message as Bitbucket would.
func prCreateEnv(t *testing.T, reject string) *[]map[string]any {
	t.Helper()
	var created []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repositories/ws/foo":
			io.WriteString(w, `{"slug":"foo","mainbranch":{"name":"main"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/repositories/ws/foo/pullrequests":
			if reject != "" {
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"type":"error","error":{"message":"Bad request","fields":{"source":["`+reject+`"]}}}`)
				return
			}
			if r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			created = append(created, body)
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"id":12,"title":"t","links":{"html":{"href":"https://bitbucket.org/ws/foo/pull-requests/12"}}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	old := bitbucketAPI
	bitbucketAPI = srv.URL
	t.Cleanup(func() { bitbucketAPI = old })

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := saveConfig(&Config{Workspace: "ws", Email: "e", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	return &created
}

// branchCheckout makes the working directory a checkout of ws/foo on the given branch.
func branchCheckout(t *testing.T, branch string) {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-b", branch)
	git(t, dir, "remote", "add", "origin", "git@bitbucket.org:ws/foo.git")
	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--allow-empty", "-m", "Add the thing")
	t.Chdir(dir)
}

func field(t *testing.T, m map[string]any, path ...string) any {
	t.Helper()
	var cur any = m
	for _, p := range path {
		next, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("no %v in %v", path, m)
		}
		cur = next[p]
	}
	return cur
}

func TestPRCreateFromCheckout(t *testing.T) {
	created := prCreateEnv(t, "")
	branchCheckout(t, "feature/x")

	out, err := runBBOut("pr", "create", "--title", " Add x ", "--body", "details", "--draft")
	if err != nil {
		t.Fatal(err)
	}
	if out != "https://bitbucket.org/ws/foo/pull-requests/12\n" {
		t.Errorf("stdout = %q, want only the URL", out)
	}
	if len(*created) != 1 {
		t.Fatalf("created %d pull requests", len(*created))
	}
	pr := (*created)[0]
	for path, want := range map[string]any{"title": "Add x", "description": "details", "draft": true} {
		if got := field(t, pr, path); got != want {
			t.Errorf("%s = %v, want %v", path, got, want)
		}
	}
	if got := field(t, pr, "source", "branch", "name"); got != "feature/x" {
		t.Errorf("source = %v", got)
	}
	if got := field(t, pr, "destination", "branch", "name"); got != "main" {
		t.Errorf("destination = %v, want the repo's main branch", got)
	}
}

func TestPRCreateExplicitBranchesOutsideCheckout(t *testing.T) {
	created := prCreateEnv(t, "")
	t.Chdir(t.TempDir())

	if err := runBB("pr", "create", "foo", "-t", "T", "-s", "topic", "-d", "release"); err != nil {
		t.Fatal(err)
	}
	pr := (*created)[0]
	if field(t, pr, "source", "branch", "name") != "topic" || field(t, pr, "destination", "branch", "name") != "release" {
		t.Errorf("branches = %v", pr)
	}
	if _, ok := pr["draft"]; ok {
		t.Error("draft should be omitted unless requested")
	}
}

func TestPRCreateErrorsWithHints(t *testing.T) {
	created := prCreateEnv(t, "")
	t.Chdir(t.TempDir())

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no repo outside a checkout", []string{"pr", "create", "-t", "T"}, "bb pr list <repo>"},
		{"no source outside a checkout", []string{"pr", "create", "foo", "-t", "T"}, "--source"},
		{"same branch", []string{"pr", "create", "foo", "-t", "T", "-s", "main"}, "different branches"},
		{"no title when not a terminal", []string{"pr", "create", "foo", "-s", "topic"}, "--title"},
		{"blank title", []string{"pr", "create", "foo", "-s", "topic", "-t", "  "}, "--title"},
		{"too many args", []string{"pr", "create", "a", "b"}, "usage"},
	}
	for _, tc := range tests {
		err := runBB(tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want error containing %q, got %v", tc.name, tc.want, err)
		}
	}
	if len(*created) != 0 {
		t.Errorf("no pull request should have been created, got %d", len(*created))
	}
}

func TestPRCreateDetachedHeadNeedsSource(t *testing.T) {
	prCreateEnv(t, "")
	branchCheckout(t, "main")
	git(t, ".", "checkout", "--detach")

	if err := runBB("pr", "create", "-t", "T"); err == nil || !strings.Contains(err.Error(), "--source") {
		t.Errorf("want hint about --source, got %v", err)
	}
}

func TestPRCreateCheckoutOfOtherRepoDoesNotSupplyBranch(t *testing.T) {
	prCreateEnv(t, "")
	branchCheckout(t, "feature/x") // a checkout of foo...

	// ...but we name a different repo, so its branch must not be assumed.
	if err := runBB("pr", "create", "bar", "-t", "T"); err == nil || !strings.Contains(err.Error(), "--source") {
		t.Errorf("want hint about --source, got %v", err)
	}
}

func TestPRCreateSurfacesAPIError(t *testing.T) {
	prCreateEnv(t, "Branch not found")
	t.Chdir(t.TempDir())

	err := runBB("pr", "create", "foo", "-t", "T", "-s", "unpushed")
	if err == nil || !strings.Contains(err.Error(), "Branch not found") || !strings.Contains(err.Error(), "source") {
		t.Errorf("want Bitbucket's message and field, got %v", err)
	}
}

func TestPRCreateNeedsWriteScope(t *testing.T) {
	found := false
	for _, s := range requiredScopes {
		found = found || s == "write:pullrequest:bitbucket"
	}
	if !found {
		t.Error("write:pullrequest:bitbucket missing from requiredScopes")
	}
}
