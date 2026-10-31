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

type approver struct {
	approved []int
	err      error
}

func (a *approver) approve(pr PullRequest) error {
	if a.err != nil {
		return a.err
	}
	a.approved = append(a.approved, pr.ID)
	return nil
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestPRSelectorEnterViewsDiffAndWOpensBrowser(t *testing.T) {
	actions := prActions{loadDiff: func(PullRequest) (string, bool, error) { return sampleDiff, false, nil }}
	prs := []PullRequest{{ID: 1, Title: "First"}, {ID: 2, Title: "Second"}}

	var m tea.Model = newPRSelector(prs, actions)
	help := m.(prSelector).View().Content
	if !strings.Contains(help, "enter view diff") || !strings.Contains(help, "w open in browser") {
		t.Errorf("list help = %q", help)
	}

	m, _ = keyPress(m, tea.KeyDown)
	m2, cmd := keyPress(m, tea.KeyEnter)
	if sel := m2.(prSelector); sel.mode != modeLoadingDiff || sel.pr.ID != 2 || sel.selected != nil || cmd == nil {
		t.Errorf("enter should start loading #2's diff: mode %v, pr %d, selected %v", sel.mode, sel.pr.ID, sel.selected)
	}

	m3, cmd := m.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	if sel := m3.(prSelector); sel.selected == nil || sel.selected.ID != 2 || !isQuit(cmd) {
		t.Errorf("w should choose #2 and quit: selected %v", sel.selected)
	}
}

func TestPRSelectorWOpensTheViewedPullRequest(t *testing.T) {
	m := openPreview(t, sampleDiff, prActions{}, 100, 20)
	m, cmd := m.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})

	if sel := m.(prSelector); sel.selected == nil || sel.selected.ID != 1 || !isQuit(cmd) {
		t.Errorf("w in the preview should choose the viewed pull request and quit: %v", sel.selected)
	}
}

func TestPRApproveAfterConfirmation(t *testing.T) {
	a := &approver{}
	m := openPreview(t, sampleDiff, prActions{approve: a.approve}, 100, 20)

	m = feed(m, "a")
	view := plain(m.(prSelector).View().Content)
	if !strings.Contains(view, "Approve #1?") || len(a.approved) != 0 {
		t.Fatalf("a should ask first, without approving:\n%s", view)
	}

	m, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if m.(prSelector).mode != modeApproving || !strings.Contains(m.(prSelector).View().Content, "Approving #1") || cmd == nil {
		t.Fatalf("y should start approving (mode %v)", m.(prSelector).mode)
	}
	m, _ = m.Update(cmd())

	sel := m.(prSelector)
	if !slicesEqual(a.approved, []int{1}) || sel.mode != modeDiff || !sel.noticeOK || !strings.Contains(sel.notice, "Approved #1") {
		t.Errorf("approved %v, mode %v, notice %q", a.approved, sel.mode, sel.notice)
	}
	if !strings.Contains(plain(sel.View().Content), "✓ Approved #1") {
		t.Error("the preview should confirm the approval")
	}
}

func slicesEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPRApproveCancelled(t *testing.T) {
	for _, key := range []rune{'n', tea.KeyEnter, tea.KeyEscape, 'a'} {
		a := &approver{}
		m := openPreview(t, sampleDiff, prActions{approve: a.approve}, 100, 20)
		m = feed(m, "a")
		m, cmd := keyPress(m, key)

		sel := m.(prSelector)
		if sel.confirmApprove || sel.mode != modeDiff || cmd != nil || len(a.approved) != 0 {
			t.Errorf("key %q should cancel the prompt without approving (confirming %v, mode %v)", key, sel.confirmApprove, sel.mode)
		}
		if strings.Contains(plain(sel.View().Content), "Approve #1?") {
			t.Errorf("key %q: the prompt should be gone", key)
		}
	}

	// ctrl+c at the prompt quits like it does anywhere else.
	m := openPreview(t, sampleDiff, prActions{approve: (&approver{}).approve}, 100, 20)
	m = feed(m, "a")
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); !isQuit(cmd) {
		t.Error("ctrl+c should still quit")
	}
}

func TestPRApproveFailureShowsReason(t *testing.T) {
	a := &approver{err: errors.New("You can't approve your own pull request")}
	m := openPreview(t, sampleDiff, prActions{approve: a.approve}, 100, 20)
	m = feed(m, "a")
	m, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m, _ = m.Update(cmd())

	sel := m.(prSelector)
	if sel.mode != modeDiff || sel.noticeOK || !strings.Contains(sel.notice, "Couldn't approve #1: You can't approve your own") {
		t.Errorf("mode %v, notice %q (ok %v)", sel.mode, sel.notice, sel.noticeOK)
	}
}

func TestPRApproveIgnoresLateOrForeignResults(t *testing.T) {
	m := openPreview(t, sampleDiff, prActions{approve: (&approver{}).approve}, 100, 20)
	m, _ = m.Update(prApproveMsg{id: 1}) // nothing is being approved
	if m.(prSelector).notice != "" {
		t.Error("an unexpected result must be ignored")
	}
}

func TestPRApproveUnavailableWithoutAction(t *testing.T) {
	m := openPreview(t, sampleDiff, prActions{}, 100, 20)
	if strings.Contains(plain(m.(prSelector).View().Content), "a approve") {
		t.Error("help shouldn't advertise approving")
	}
	m = feed(m, "a")
	if m.(prSelector).confirmApprove {
		t.Error("a should do nothing without an approve action")
	}

	m = openPreview(t, sampleDiff, prActions{approve: (&approver{}).approve}, 100, 20)
	if help := plain(m.(prSelector).View().Content); !strings.Contains(help, "a approve") || !strings.Contains(help, "w web") {
		t.Errorf("help should list a and w:\n%s", help)
	}
}

func TestPRDiffHelpKeepsQuitHintVisibleAt100Columns(t *testing.T) {
	m := openPreviewWithComments(t, &commentSource{}, prActions{approve: (&approver{}).approve, postComment: (&poster{}).post})
	lines := strings.Split(plain(m.(prSelector).View().Content), "\n")
	footer := lines[len(lines)-1]
	if !strings.HasPrefix(footer, "q back") || strings.Contains(footer, "…") {
		t.Errorf("the full help should fit in 100 columns, starting with q back: %q", footer)
	}
}

func TestApprovePullRequest(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repositories/ws/foo/pullrequests/7/approve":
			gotMethod, gotPath = r.Method, r.URL.Path
			io.WriteString(w, `{"approved":true}`)
		case "/repositories/ws/foo/pullrequests/8/approve":
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"type":"error","error":{"message":"You can't approve your own pull request."}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	old := bitbucketAPI
	bitbucketAPI = srv.URL
	defer func() { bitbucketAPI = old }()
	cfg := &Config{Workspace: "ws", Email: "e", Token: "t"}

	if err := approvePullRequest(t.Context(), cfg, "foo", 7); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotPath != "/repositories/ws/foo/pullrequests/7/approve" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if err := approvePullRequest(t.Context(), cfg, "foo", 8); err == nil || !strings.Contains(err.Error(), "own pull request") {
		t.Errorf("want Bitbucket's reason, got %v", err)
	}
	if err := approvePullRequest(t.Context(), cfg, "foo", 99); err == nil || !strings.Contains(err.Error(), "#99") {
		t.Errorf("missing PR: %v", err)
	}
}
