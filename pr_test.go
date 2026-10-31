package bb

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// prEnv serves two pages of pull requests for ws/foo, records the queries it was sent, and
// saves a config for workspace "ws".
func prEnv(t *testing.T) *[]url.Values {
	t.Helper()
	var queries []url.Values

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repositories/ws/foo/pullrequests" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		queries = append(queries, r.URL.Query())
		pr := func(id int, title string) string {
			return fmt.Sprintf(`{"id":%d,"title":%q,"state":"OPEN","author":{"display_name":"Ann"},`+
				`"source":{"branch":{"name":"feat-%d"}},"updated_on":"2026-01-02T03:04:05Z",`+
				`"links":{"html":{"href":"https://bitbucket.org/ws/foo/pull-requests/%d"}}}`, id, title, id, id)
		}
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprintf(w, `{"values":[%s]}`, pr(2, "Second"))
			return
		}
		fmt.Fprintf(w, `{"values":[%s],"next":"%s%s?%s&page=2"}`, pr(1, "First"), srv.URL, r.URL.Path, r.URL.RawQuery)
	}))
	t.Cleanup(srv.Close)

	old := bitbucketAPI
	bitbucketAPI = srv.URL
	t.Cleanup(func() { bitbucketAPI = old })

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := saveConfig(&Config{Workspace: "ws", Email: "e", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	return &queries
}

func TestPRWithoutSubcommandShowsHelp(t *testing.T) {
	out, err := runBBOut("pr")
	if err != nil || !strings.Contains(out, "list") {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

func TestPRListPrintsTableWhenNotInteractive(t *testing.T) {
	queries := prEnv(t)

	for _, name := range []string{"list", "ls"} {
		*queries = nil
		out, err := runBBOut("pr", name, "foo")
		if err != nil {
			t.Fatalf("pr %s: %v", name, err)
		}
		if !strings.Contains(out, "#2") || !strings.Contains(out, "Second") {
			t.Errorf("pr %s: table missing the second page:\n%s", name, out)
		}
		if len(*queries) != 2 {
			t.Fatalf("pr %s: want 2 page requests, got %d", name, len(*queries))
		}
	}
	if got := (*queries)[0]["state"]; len(got) != 1 || got[0] != "OPEN" {
		t.Errorf("default state = %v, want [OPEN]", got)
	}
}

func TestPRListStateFlag(t *testing.T) {
	queries := prEnv(t)
	if err := runBB("pr", "list", "foo", "--state", "merged,Declined"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join((*queries)[0]["state"], ","); got != "MERGED,DECLINED" {
		t.Errorf("state = %q", got)
	}

	if err := runBB("pr", "list", "foo", "--state", "bogus"); err == nil || !strings.Contains(err.Error(), "open") {
		t.Errorf("want invalid-state error listing choices, got %v", err)
	}
}

func TestPRListErrors(t *testing.T) {
	prEnv(t)
	if err := runBB("pr", "list", "missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown repo: got %v", err)
	}
	if err := runBB("pr", "list", "a", "b"); err == nil {
		t.Error("two repos should be rejected")
	}

	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no config
	if err := runBB("pr", "list", "foo"); err == nil || !strings.Contains(err.Error(), "bb configure") {
		t.Errorf("unconfigured: got %v", err)
	}
}

func TestPRListOutsideCheckoutFailsWithHint(t *testing.T) {
	prEnv(t)
	t.Chdir(t.TempDir()) // not a git repository
	err := runBB("pr", "list")
	if err == nil || !strings.Contains(err.Error(), "bb pr list <repo>") {
		t.Errorf("want hint to name a repo, got %v", err)
	}
}

func TestPRListInfersRepoFromCheckout(t *testing.T) {
	queries := prEnv(t)
	dir := t.TempDir()
	git(t, dir, "init", "-b", "main")
	git(t, dir, "remote", "add", "origin", "git@bitbucket.org:ws/foo.git")
	t.Chdir(dir)

	if err := runBB("pr", "list"); err != nil {
		t.Fatal(err)
	}
	if len(*queries) == 0 {
		t.Error("expected the pull requests of ws/foo to be fetched")
	}
}

func TestCheckoutRepoSlug(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-b", "main")
	git(t, dir, "remote", "add", "a-fork", "https://bitbucket.org/ws/other.git")
	git(t, dir, "remote", "add", "origin", "https://me@bitbucket.org/ws/foo")
	git(t, dir, "remote", "add", "github", "git@github.com:ws/bar.git")

	if slug, ok := checkoutRepoSlug(t.Context(), "git", dir, "WS"); !ok || slug != "foo" {
		t.Errorf("got %q, %v; want origin's repo foo", slug, ok)
	}
	if _, ok := checkoutRepoSlug(t.Context(), "git", dir, "elsewhere"); ok {
		t.Error("a different workspace must not match")
	}
}

func TestBitbucketRepoSlug(t *testing.T) {
	tests := []struct {
		url, slug string
		ok        bool
	}{
		{"git@bitbucket.org:ws/foo.git", "foo", true},
		{"https://user@bitbucket.org/ws/foo.git", "foo", true},
		{"ssh://git@bitbucket.org/ws/foo", "foo", true},
		{"git@github.com:ws/foo.git", "", false},
		{"git@bitbucket.org:other/foo.git", "", false},
		{"git@bitbucket.org:ws/foo/bar.git", "", false},
		{"/some/local/path", "", false},
	}
	for _, tc := range tests {
		slug, ok := bitbucketRepoSlug(tc.url, "ws")
		if slug != tc.slug || ok != tc.ok {
			t.Errorf("bitbucketRepoSlug(%q) = %q, %v; want %q, %v", tc.url, slug, ok, tc.slug, tc.ok)
		}
	}
}

func TestParsePRStates(t *testing.T) {
	got, err := parsePRStates("all")
	if err != nil || len(got) != 4 || got[0] != "OPEN" {
		t.Errorf("all = %v, %v", got, err)
	}
	if _, err := parsePRStates(" , "); err == nil {
		t.Error("empty state list should fail")
	}
}

func TestPRSelector(t *testing.T) {
	prs := []PullRequest{{ID: 1, Title: "First"}, {ID: 2, Title: "Second"}}

	press := func(m prSelector, key rune) prSelector {
		next, _ := m.Update(tea.KeyPressMsg{Code: key})
		return next.(prSelector)
	}

	m := press(press(newPRSelector(prs, nil), tea.KeyDown), tea.KeyEnter)
	if m.selected == nil || m.selected.ID != 2 {
		t.Errorf("down+enter selected %+v, want #2", m.selected)
	}

	for _, key := range []rune{'q', tea.KeyEscape} {
		if m := press(newPRSelector(prs, nil), key); m.selected != nil {
			t.Errorf("key %q should cancel, selected %+v", key, m.selected)
		}
	}
	if !strings.Contains(newPRSelector(prs, nil).View().Content, "Second") {
		t.Error("view should list the pull requests")
	}
}

func TestOpenPullRequestPrintsURLAndTriesBrowser(t *testing.T) {
	pr := &PullRequest{ID: 7}
	pr.Links.HTML.Href = "https://bitbucket.org/ws/foo/pull-requests/7"

	var out, errOut bytes.Buffer
	var opened string
	err := openPullRequest(&out, &errOut, pr, func(u string) error { opened = u; return nil })
	if err != nil || out.String() != pr.URL()+"\n" || opened != pr.URL() || errOut.Len() != 0 {
		t.Errorf("out=%q opened=%q errOut=%q err=%v", out.String(), opened, errOut.String(), err)
	}

	// No browser available: still succeeds, the URL is on stdout and the problem on stderr.
	out.Reset()
	err = openPullRequest(&out, &errOut, pr, func(string) error { return errors.New("no xdg-open") })
	if err != nil || out.String() != pr.URL()+"\n" || !strings.Contains(errOut.String(), "no xdg-open") {
		t.Errorf("out=%q errOut=%q err=%v", out.String(), errOut.String(), err)
	}
}
