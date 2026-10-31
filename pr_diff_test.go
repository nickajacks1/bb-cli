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

func keyPress(m tea.Model, key rune) (tea.Model, tea.Cmd) {
	return m.Update(tea.KeyPressMsg{Code: key})
}

// feed sends a sequence of single-character keys, ignoring the commands they return.
func feed(m tea.Model, keys string) tea.Model {
	for _, k := range keys {
		m, _ = m.Update(tea.KeyPressMsg{Code: k, Text: string(k)})
	}
	return m
}

// openPreview opens the diff preview of the first pull request at the given terminal size.
func openPreview(t *testing.T, diff string, actions prActions, width, height int) tea.Model {
	t.Helper()
	if actions.loadDiff == nil {
		actions.loadDiff = func(PullRequest) (string, bool, error) { return diff, false, nil }
	}
	var m tea.Model = newPRSelector([]PullRequest{{ID: 1, Title: "First"}, {ID: 2, Title: "Second"}}, actions)
	m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m, cmd := keyPress(m, 'v')
	m, _ = m.Update(cmd())
	if m.(prSelector).mode != modeDiff {
		t.Fatalf("preview didn't open: mode %v, notice %q", m.(prSelector).mode, m.(prSelector).notice)
	}
	return m
}

func TestPRSelectorDiffPreview(t *testing.T) {
	var loaded []int
	load := func(pr PullRequest) (string, bool, error) {
		loaded = append(loaded, pr.ID)
		return sampleDiff, false, nil
	}

	var m tea.Model = newPRSelector([]PullRequest{{ID: 1, Title: "First"}, {ID: 2, Title: "Second"}}, prActions{loadDiff: load})
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
	view := plain(m.(prSelector).View().Content)
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

func TestPRSelectorDiffCursorMoves(t *testing.T) {
	m := openPreview(t, strings.Repeat("+line\n", 200), prActions{}, 80, 10)
	v := func() diffViewer { return m.(prSelector).viewer }

	if v().cursor != 0 {
		t.Fatal("cursor should start at the top")
	}
	m, _ = keyPress(m, 'G')
	if v().cursor != len(v().rows)-1 {
		t.Error("G should jump to the last row")
	}
	m, _ = keyPress(m, 'g')
	if v().cursor != 0 {
		t.Error("g should jump to the first row")
	}
	m, _ = keyPress(m, 'j')
	if v().cursor != 1 {
		t.Error("j should move the cursor down a row")
	}
}

func TestPRSelectorSplitToggle(t *testing.T) {
	m := openPreview(t, viewerDiff, prActions{}, 140, 30)
	if m.(prSelector).viewer.rowsSplit {
		t.Fatal("the preview should start unified")
	}

	m = feed(m, "s")
	sel := m.(prSelector)
	if !sel.viewer.rowsSplit || !strings.Contains(plain(sel.View().Content), "│") || !strings.Contains(plain(sel.View().Content), "split") {
		t.Errorf("s should switch to split:\n%s", plain(sel.View().Content))
	}

	// The preference sticks for the next preview.
	m = feed(m, "q")
	m, cmd := keyPress(m, 'v')
	m, _ = m.Update(cmd())
	if !m.(prSelector).viewer.rowsSplit {
		t.Error("the split preference should carry over to the next preview")
	}

	m = feed(m, "s")
	if m.(prSelector).viewer.rowsSplit {
		t.Error("s again should return to unified")
	}
}

func TestPRSelectorSplitNeedsWidth(t *testing.T) {
	m := openPreview(t, viewerDiff, prActions{}, 70, 30)
	m = feed(m, "s")
	sel := m.(prSelector)
	if sel.viewer.rowsSplit || !strings.Contains(sel.notice, "columns") || sel.noticeOK {
		t.Errorf("narrow terminal should keep unified and explain why (notice %q)", sel.notice)
	}

	// Widening the terminal then makes the preferred split layout take effect.
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	if !m.(prSelector).viewer.rowsSplit {
		t.Error("widening should enable the requested split layout")
	}
}

func TestPRSelectorDiffErrorReturnsToList(t *testing.T) {
	var m tea.Model = newPRSelector([]PullRequest{{ID: 5}}, prActions{loadDiff: func(PullRequest) (string, bool, error) {
		return "", false, errors.New("boom")
	}})
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
	var m tea.Model = newPRSelector([]PullRequest{{ID: 1}}, prActions{loadDiff: func(PullRequest) (string, bool, error) { return sampleDiff, false, nil }})
	m, cmd := keyPress(m, 'v')
	m, _ = keyPress(m, 'q') // give up while loading
	m, _ = m.Update(cmd())  // the result arrives late
	if m.(prSelector).mode != modeList {
		t.Error("a late diff must not pull the user back into the preview")
	}
}

func TestPRSelectorWithoutActionsIgnoresKeys(t *testing.T) {
	var m tea.Model = newPRSelector([]PullRequest{{ID: 1}}, prActions{})
	for _, k := range "vc" {
		var cmd tea.Cmd
		m, cmd = keyPress(m, k)
		if m.(prSelector).mode != modeList || cmd != nil {
			t.Errorf("%q should do nothing without its action", k)
		}
	}
	help := m.(prSelector).View().Content
	if strings.Contains(help, "view diff") || strings.Contains(help, "comment") {
		t.Errorf("help shouldn't advertise missing actions: %q", help)
	}
}
