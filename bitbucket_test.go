package bb

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListRepositoriesPaginatesAndSorts(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "me@example.com" || pass != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/repositories/ws" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"values":[{"slug":"alpha","is_private":false}]}`)
			return
		}
		fmt.Fprintf(w, `{"values":[{"slug":"zulu","is_private":true},{"slug":"mike"}],"next":"%s/repositories/ws?page=2"}`, srv.URL)
	}))
	defer srv.Close()

	old := bitbucketAPI
	bitbucketAPI = srv.URL
	defer func() { bitbucketAPI = old }()

	cfg := &Config{Workspace: "ws", Email: "me@example.com", Token: "tok"}
	repos, err := listRepositories(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, r := range repos {
		got = append(got, r.Slug)
	}
	if want := "alpha,mike,zulu"; strings.Join(got, ",") != want {
		t.Errorf("slugs = %v, want %s", got, want)
	}
}

func TestListRepositoriesErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	old := bitbucketAPI
	bitbucketAPI = srv.URL
	defer func() { bitbucketAPI = old }()

	_, err := listRepositories(context.Background(), &Config{Workspace: "ws"}, nil)
	if err == nil || !strings.Contains(err.Error(), "scope") {
		t.Errorf("err = %v, want scope error", err)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("a\nb  c", 10); got != "a b c" {
		t.Errorf("got %q", got)
	}
	if got := truncate("héllo wörld", 5); got != "héll…" {
		t.Errorf("got %q", got)
	}
	// Titles, names and descriptions come from other people: no terminal escapes may get through.
	if got := truncate("evil\x1b]52;c;cm0gLXJm\x07 title", 80); strings.ContainsAny(got, "\x1b\x07") {
		t.Errorf("escape sequence kept: %q", got)
	}
}

func TestInlineDescriptionSanitizesPath(t *testing.T) {
	if got := inlineDescription(inlineRef{path: "a\x1b[2Jb.go", to: 3}); strings.Contains(got, "\x1b") {
		t.Errorf("escape sequence kept: %q", got)
	}
}
