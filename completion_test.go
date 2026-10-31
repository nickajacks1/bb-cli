package bb

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func complete(t *testing.T, args ...string) []string {
	t.Helper()
	var buf bytes.Buffer
	app := NewApp()
	app.Writer = &buf
	if err := app.Run(context.Background(), append([]string{"bb"}, args...)); err != nil {
		t.Fatal(err)
	}
	return strings.Fields(buf.String())
}

func TestRepoNameCompletion(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// No config or cache: nothing to offer, and no network.
	if got := complete(t, "clone", "--generate-shell-completion"); len(got) != 0 {
		t.Fatalf("expected nothing without a cache, got %v", got)
	}

	if _, err := saveConfig(&Config{Workspace: "ws", Email: "e", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := saveRepoCache("ws", []Repository{{Slug: "alpha"}, {Slug: "bravo"}}); err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(complete(t, "clone", "--generate-shell-completion"), ","); got != "alpha,bravo" {
		t.Errorf("clone completion = %q", got)
	}
	// clone takes one repo: nothing after it's been given.
	if got := complete(t, "clone", "alpha", "--generate-shell-completion"); len(got) != 0 {
		t.Errorf("clone after arg = %v", got)
	}
	// Flags still complete.
	if got := strings.Join(complete(t, "ls", "--so", "--generate-shell-completion"), " "); !strings.Contains(got, "--sort") {
		t.Errorf("flag completion = %q", got)
	}
	// ls takes many, but doesn't repeat ones already typed.
	if got := strings.Join(complete(t, "ls", "alpha", "--generate-shell-completion"), ","); got != "bravo" {
		t.Errorf("ls completion = %q", got)
	}
}

func TestFilterRepositories(t *testing.T) {
	repos := []Repository{
		{Slug: "a"}, {Slug: "gold-foo-bar"}, {Slug: "foo-bar"}, {Slug: "x-foo-"}, {Slug: "afoob"}, {Slug: "a.b"},
	}
	slugs := func(rs []Repository) string {
		var out []string
		for _, r := range rs {
			out = append(out, r.Slug)
		}
		return strings.Join(out, ",")
	}

	tests := []struct {
		patterns []string
		want     string
		missing  string
	}{
		{[]string{"a"}, "a", ""},                         // exact
		{[]string{"A"}, "a", ""},                         // case-insensitive
		{[]string{"*-foo-*"}, "gold-foo-bar,x-foo-", ""}, // * also matches empty
		{[]string{"foo*"}, "foo-bar", ""},                // anchored at the start
		{[]string{"*foo*"}, "gold-foo-bar,foo-bar,x-foo-,afoob", ""},
		{[]string{"a.b"}, "a.b", ""},                                        // other regexp chars are literal
		{[]string{"a?b"}, "", "a?b"},                                        // ? is not a wildcard
		{[]string{"gold*", "foo-bar", "gold*"}, "gold-foo-bar,foo-bar", ""}, // no duplicates
		{[]string{"*", "nope*"}, "a,gold-foo-bar,foo-bar,x-foo-,afoob,a.b", "nope*"},
	}
	for _, tc := range tests {
		got, missing := filterRepositories(repos, tc.patterns)
		if slugs(got) != tc.want || strings.Join(missing, ",") != tc.missing {
			t.Errorf("%v: got %q missing %q, want %q missing %q", tc.patterns, slugs(got), missing, tc.want, tc.missing)
		}
	}
}
