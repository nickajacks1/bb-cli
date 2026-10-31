package bb

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// openEnv fakes repositories foo (with an html link), bar (without one) and a 401 for "locked",
// saves a config for workspace "ws", and records the URLs the browser is asked to open.
func openEnv(t *testing.T) (opened *[]string, requests *int) {
	t.Helper()
	opened, requests = new([]string), new(int)
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*requests++
		mu.Unlock()
		switch r.URL.Path {
		case "/repositories/ws/foo":
			io.WriteString(w, `{"slug":"foo","links":{"html":{"href":"https://bitbucket.org/ws/foo"}}}`)
		case "/repositories/ws/bar":
			io.WriteString(w, `{"slug":"bar"}`)
		case "/repositories/ws/locked":
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	oldAPI, oldOpen := bitbucketAPI, launchBrowser
	bitbucketAPI = srv.URL
	launchBrowser = func(u string) error {
		*opened = append(*opened, u)
		return nil
	}
	t.Cleanup(func() { bitbucketAPI, launchBrowser = oldAPI, oldOpen })

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := saveConfig(&Config{Workspace: "ws", Email: "e", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	return opened, requests
}

func TestOpenOpensEveryRepoInOrder(t *testing.T) {
	opened, _ := openEnv(t)

	out, err := runBBOut("open", "bar", "foo")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://bitbucket.org/ws/bar", "https://bitbucket.org/ws/foo"} // bar has no link: built from the slug
	if !slices.Equal(*opened, want) {
		t.Errorf("opened %v, want %v", *opened, want)
	}
	if out != strings.Join(want, "\n")+"\n" {
		t.Errorf("stdout = %q, want the URLs one per line", out)
	}
}

func TestOpenIgnoresRepeatedNames(t *testing.T) {
	opened, requests := openEnv(t)
	if err := runBB("open", "foo", "foo", "bar", "foo"); err != nil {
		t.Fatal(err)
	}
	if len(*opened) != 2 || *requests != 2 {
		t.Errorf("opened %v after %d requests; each repo should be looked up and opened once", *opened, *requests)
	}
}

func TestOpenOpensNothingIfAnyRepoIsMissing(t *testing.T) {
	opened, _ := openEnv(t)
	out, err := runBBOut("open", "foo", "nope", "bar", "gone")

	if err == nil || !strings.Contains(err.Error(), "nope, gone") || !strings.Contains(err.Error(), "nothing was opened") {
		t.Errorf("want an error naming both missing repos, got %v", err)
	}
	if len(*opened) != 0 || out != "" {
		t.Errorf("nothing should be opened or printed, got %v / %q", *opened, out)
	}
}

func TestOpenReportsOtherErrorsOnce(t *testing.T) {
	opened, _ := openEnv(t)
	err := runBB("open", "foo", "locked")
	if err == nil || !strings.Contains(err.Error(), "rejected the email/token") || strings.Count(err.Error(), "401") != 1 {
		t.Errorf("want the auth error once, got %v", err)
	}
	if len(*opened) != 0 {
		t.Errorf("opened %v", *opened)
	}
}

func TestOpenNeedsARepoAndConfig(t *testing.T) {
	openEnv(t)
	if err := runBB("open"); err == nil || !strings.Contains(err.Error(), "usage: bb open") {
		t.Errorf("no args: got %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := runBB("open", "foo"); err == nil || !strings.Contains(err.Error(), "bb configure") {
		t.Errorf("unconfigured: got %v", err)
	}
}

func TestOpenURLsKeepsGoingWhenABrowserLaunchFails(t *testing.T) {
	var tried []string
	var out strings.Builder
	err := openURLs(&out, []string{"a", "b", "c"}, func(u string) error {
		tried = append(tried, u)
		if u == "b" {
			return errors.New("no xdg-open")
		}
		return nil
	})

	if !slices.Equal(tried, []string{"a", "b", "c"}) || out.String() != "a\nb\nc\n" {
		t.Errorf("tried %v, printed %q; a failure must not stop the rest", tried, out.String())
	}
	if err == nil || !strings.Contains(err.Error(), "1 of 3") || !strings.Contains(err.Error(), "no xdg-open") {
		t.Errorf("want a summary of the failure, got %v", err)
	}
}

func TestOpenCompletesRepoNamesWithoutRepeats(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if _, err := saveConfig(&Config{Workspace: "ws", Email: "e", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := saveRepoCache("ws", []Repository{{Slug: "alpha"}, {Slug: "bravo"}, {Slug: "charlie"}}); err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(complete(t, "open", "--generate-shell-completion"), ","); got != "alpha,bravo,charlie" {
		t.Errorf("open completion = %q", got)
	}
	if got := strings.Join(complete(t, "open", "alpha", "charlie", "--generate-shell-completion"), ","); got != "bravo" {
		t.Errorf("open completion after two names = %q; it takes many but shouldn't repeat", got)
	}
}

func TestRepositoryWebURL(t *testing.T) {
	var r Repository
	r.Slug = "my repo"
	if got := r.webURL("my ws"); got != "https://bitbucket.org/my%20ws/my%20repo" {
		t.Errorf("built URL = %q", got)
	}
	r.Links.HTML.Href = "https://example.test/x"
	if got := r.webURL("ws"); got != "https://example.test/x" {
		t.Errorf("a link from the API wins, got %q", got)
	}
}
