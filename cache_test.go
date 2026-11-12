package bb

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRepoCacheRoundTripAndExpiry(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if c := loadRepoCache("ws"); c != nil {
		t.Fatalf("expected no cache, got %+v", c)
	}

	if err := saveRepoCache("ws", []Repository{{Slug: "a", IsPrivate: true}}); err != nil {
		t.Fatal(err)
	}
	c := loadRepoCache("ws")
	if c == nil || len(c.Repos) != 1 || c.Repos[0].Slug != "a" || !c.Repos[0].IsPrivate {
		t.Fatalf("bad round trip: %+v", c)
	}
	if c.expired(time.Now()) {
		t.Error("fresh cache reported expired")
	}
	if c.expired(c.FetchedAt.Add(cacheTTL - time.Hour)) {
		t.Error("cache just under the TTL reported expired")
	}
	if !c.expired(c.FetchedAt.Add(cacheTTL + time.Hour)) {
		t.Error("cache past the TTL not reported expired")
	}

	path, _ := repoCachePath("ws")
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("cache perms = %v, err = %v", info.Mode().Perm(), err)
	}
}

func TestCorruptCacheIsIgnored(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path, _ := repoCachePath("ws")
	os.MkdirAll(dirOf(path), 0o700)
	os.WriteFile(path, []byte("{not json"), 0o600)
	if c := loadRepoCache("ws"); c != nil {
		t.Errorf("expected nil for corrupt cache, got %+v", c)
	}
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}

func TestLoadRepositoriesCacheBehavior(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"size":1,"values":[{"slug":"fresh"}]}`)
	}))
	defer srv.Close()
	old := bitbucketAPI
	bitbucketAPI = srv.URL
	defer func() { bitbucketAPI = old }()

	cfg := &Config{Workspace: "ws", Email: "e", Token: "t"}
	ctx := context.Background()

	// No cache: fetches and populates it.
	repos, cachedAt, err := loadRepositories(ctx, cfg, false, false)
	if err != nil || calls != 1 || !cachedAt.IsZero() || repos[0].Slug != "fresh" {
		t.Fatalf("first load: repos=%v cachedAt=%v calls=%d err=%v", repos, cachedAt, calls, err)
	}

	// Fresh cache: no fetch.
	_, cachedAt, err = loadRepositories(ctx, cfg, false, false)
	if err != nil || calls != 1 || cachedAt.IsZero() {
		t.Fatalf("cached load: cachedAt=%v calls=%d err=%v", cachedAt, calls, err)
	}

	// --refresh forces a fetch.
	if _, _, err = loadRepositories(ctx, cfg, false, true); err != nil || calls != 2 {
		t.Fatalf("refresh: calls=%d err=%v", calls, err)
	}

	// Age the cache past the TTL: refetch, unless --stale.
	path, _ := repoCachePath("ws")
	old2 := time.Now().Add(-cacheTTL - time.Hour)
	data := fmt.Sprintf(`{"fetched_at":%q,"repos":[{"slug":"old"}]}`, old2.Format(time.RFC3339))
	os.WriteFile(path, []byte(data), 0o600)

	repos, cachedAt, err = loadRepositories(ctx, cfg, true, false)
	if err != nil || calls != 2 || repos[0].Slug != "old" || cachedAt.IsZero() {
		t.Fatalf("stale: repos=%v calls=%d err=%v", repos, calls, err)
	}
	repos, cachedAt, err = loadRepositories(ctx, cfg, false, false)
	if err != nil || calls != 3 || repos[0].Slug != "fresh" || !cachedAt.IsZero() {
		t.Fatalf("expired: repos=%v calls=%d err=%v", repos, calls, err)
	}
}

func TestListRepositoriesReportsProgress(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"size":3,"values":[{"slug":"c"}]}`)
			return
		}
		fmt.Fprintf(w, `{"size":3,"values":[{"slug":"a"},{"slug":"b"}],"next":"%s/repositories/ws?page=2"}`, srv.URL)
	}))
	defer srv.Close()
	old := bitbucketAPI
	bitbucketAPI = srv.URL
	defer func() { bitbucketAPI = old }()

	var got [][2]int
	_, err := listRepositories(context.Background(), &Config{Workspace: "ws"}, func(f, total int) {
		got = append(got, [2]int{f, total})
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != "[[2 3] [3 3]]" {
		t.Errorf("progress = %v", got)
	}
}

func TestSortRepositories(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC) }
	repos := []Repository{
		{Slug: "b", UpdatedOn: d(2)},
		{Slug: "c", UpdatedOn: d(3)},
		{Slug: "a", UpdatedOn: d(2)},
	}

	slugs := func() string {
		s := ""
		for _, r := range repos {
			s += r.Slug
		}
		return s
	}

	sortRepositories(repos, sortUpdated)
	if got := slugs(); got != "cab" { // newest first, ties by name
		t.Errorf("updated sort = %s, want cab", got)
	}
	sortRepositories(repos, sortName)
	if got := slugs(); got != "abc" {
		t.Errorf("name sort = %s, want abc", got)
	}
}

func TestListPrintsToTheCommandWriter(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"size":2,"values":[{"slug":"beta"},{"slug":"alpha"}]}`)
	}))
	defer srv.Close()
	old := bitbucketAPI
	bitbucketAPI = srv.URL
	defer func() { bitbucketAPI = old }()
	if _, err := saveConfig(&Config{Workspace: "ws", Email: "e", Token: "t"}); err != nil {
		t.Fatal(err)
	}

	out, err := runBBOut("ls", "-q")
	if err != nil || out != "alpha\nbeta\n" {
		t.Errorf("ls -q: out = %q, err = %v", out, err)
	}
	out, err = runBBOut("ls")
	if err != nil || !strings.Contains(out, "NAME") || !strings.Contains(out, "alpha") {
		t.Errorf("ls: out = %q, err = %v", out, err)
	}
}
