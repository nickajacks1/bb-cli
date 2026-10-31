package bb

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

const sampleDiff = "diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1,2 +1,2 @@\n context\n-removed line\n+added line\n"

func keyPress(m tea.Model, key rune) (tea.Model, tea.Cmd) {
	return m.Update(tea.KeyPressMsg{Code: key})
}

func TestPRSelectorDiffPreview(t *testing.T) {
	prs := []PullRequest{{ID: 1, Title: "First"}, {ID: 2, Title: "Second"}}
	var loaded []int
	load := func(pr PullRequest) (string, bool, error) {
		loaded = append(loaded, pr.ID)
		return sampleDiff, false, nil
	}

	var m tea.Model = newPRSelector(prs, load)
	m, _ = keyPress(m, tea.KeyDown)
	m, cmd := keyPress(m, 'v')
	if got := m.(prSelector).mode; got != modeLoadingDiff || cmd == nil {
		t.Fatalf("v should start loading (mode %v, cmd %v)", got, cmd)
	}
	if !strings.Contains(m.(prSelector).View().Content, "Loading the diff of #2") {
		t.Error("view should say the diff is loading")
	}

	m, _ = m.Update(cmd()) // deliver the fetched diff
	if len(loaded) != 1 || loaded[0] != 2 {
		t.Errorf("loaded %v, want the highlighted pull request #2", loaded)
	}
	view := m.(prSelector).View().Content
	for _, want := range []string{"#2 Second", "removed line", "added line"} {
		if !strings.Contains(view, want) {
			t.Errorf("preview missing %q:\n%s", want, view)
		}
	}

	// q leaves the preview for the list instead of quitting; a second q quits.
	m, cmd = keyPress(m, 'q')
	if m.(prSelector).mode != modeList || cmd != nil {
		t.Errorf("q in preview should return to the list without quitting (mode %v)", m.(prSelector).mode)
	}
	if _, cmd = keyPress(m, 'q'); cmd == nil {
		t.Error("q in the list should quit")
	}
	if m.(prSelector).selected != nil {
		t.Error("previewing must not select the pull request")
	}
}

func TestPRSelectorDiffScrolls(t *testing.T) {
	long := strings.Repeat("+line\n", 200)
	var m tea.Model = newPRSelector([]PullRequest{{ID: 1}}, func(PullRequest) (string, bool, error) { return long, false, nil })
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m, cmd := keyPress(m, 'v')
	m, _ = m.Update(cmd())

	if !m.(prSelector).diff.AtTop() {
		t.Fatal("preview should start at the top")
	}
	m, _ = keyPress(m, 'G')
	if !m.(prSelector).diff.AtBottom() {
		t.Error("G should jump to the bottom")
	}
	m, _ = keyPress(m, 'g')
	if !m.(prSelector).diff.AtTop() {
		t.Error("g should jump to the top")
	}
	m, _ = keyPress(m, 'j')
	if m.(prSelector).diff.AtTop() {
		t.Error("j should scroll down")
	}
}

func TestPRSelectorDiffErrorReturnsToList(t *testing.T) {
	var m tea.Model = newPRSelector([]PullRequest{{ID: 5}}, func(PullRequest) (string, bool, error) {
		return "", false, errors.New("boom")
	})
	m, cmd := keyPress(m, 'v')
	m, _ = m.Update(cmd())

	sel := m.(prSelector)
	if sel.mode != modeList || !strings.Contains(sel.View().Content, "Couldn't load the diff of #5: boom") {
		t.Errorf("want the list with an error notice, got mode %v:\n%s", sel.mode, sel.View().Content)
	}
	m, _ = keyPress(m, tea.KeyDown)
	if m.(prSelector).notice != "" {
		t.Error("the notice should clear on the next key")
	}
}

func TestPRSelectorIgnoresDiffAfterCancel(t *testing.T) {
	var m tea.Model = newPRSelector([]PullRequest{{ID: 1}}, func(PullRequest) (string, bool, error) { return sampleDiff, false, nil })
	m, cmd := keyPress(m, 'v')
	m, _ = keyPress(m, 'q') // give up while loading
	m, _ = m.Update(cmd())  // the result arrives late
	if m.(prSelector).mode != modeList {
		t.Error("a late diff must not pull the user back into the preview")
	}
}

func TestPRSelectorWithoutLoaderIgnoresV(t *testing.T) {
	var m tea.Model = newPRSelector([]PullRequest{{ID: 1}}, nil)
	m, cmd := keyPress(m, 'v')
	if m.(prSelector).mode != modeList || cmd != nil {
		t.Error("v should do nothing without a diff loader")
	}
	if strings.Contains(m.(prSelector).View().Content, "view diff") {
		t.Error("help shouldn't advertise v without a loader")
	}
}

func TestColorizeDiffKeepsText(t *testing.T) {
	got := colorizeDiff(sampleDiff + "\tindented\r\n")
	if !strings.Contains(got, "\x1b[") {
		t.Error("diff should be colored")
	}
	plain := stripANSI(got)
	if plain != strings.ReplaceAll(strings.ReplaceAll(sampleDiff+"\tindented\r\n", "\t", "    "), "\r", "") {
		t.Errorf("colorizing changed the text: %q", plain)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestGetPullRequestDiff(t *testing.T) {
	var accept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		switch r.URL.Path {
		case "/repositories/ws/foo/pullrequests/3/diff":
			io.WriteString(w, sampleDiff)
		case "/repositories/ws/foo/pullrequests/4/diff":
			io.WriteString(w, strings.Repeat("x", maxDiffBytes+10))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	old := bitbucketAPI
	bitbucketAPI = srv.URL
	defer func() { bitbucketAPI = old }()
	cfg := &Config{Workspace: "ws", Email: "e", Token: "t"}

	diff, truncated, err := getPullRequestDiff(t.Context(), cfg, "foo", 3)
	if err != nil || truncated || diff != sampleDiff {
		t.Errorf("diff = %q, truncated %v, err %v", diff, truncated, err)
	}
	if strings.Contains(accept, "json") && !strings.Contains(accept, "text/plain") {
		t.Errorf("diff must be requested as text, Accept = %q", accept)
	}

	diff, truncated, err = getPullRequestDiff(t.Context(), cfg, "foo", 4)
	if err != nil || !truncated || len(diff) != maxDiffBytes {
		t.Errorf("big diff: len %d, truncated %v, err %v", len(diff), truncated, err)
	}

	if _, _, err = getPullRequestDiff(t.Context(), cfg, "foo", 99); err == nil || !strings.Contains(err.Error(), "#99") {
		t.Errorf("missing diff: got %v", err)
	}
}
