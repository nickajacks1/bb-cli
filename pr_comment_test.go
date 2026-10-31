package bb

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

var ctrlS = tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}

// poster records the comments it is asked to post and fails the first `fail` attempts.
type poster struct {
	posted []newComment
	fail   int
}

func (p *poster) post(_ PullRequest, c newComment) error {
	if p.fail > 0 {
		p.fail--
		return errors.New("503 try later")
	}
	p.posted = append(p.posted, c)
	return nil
}

// submit presses ctrl+s and delivers the result of posting.
func submit(t *testing.T, m tea.Model) tea.Model {
	t.Helper()
	m, cmd := m.Update(ctrlS)
	if cmd == nil {
		t.Fatalf("ctrl+s should post (mode %v, err %q)", m.(prSelector).mode, m.(prSelector).comment.err)
	}
	if m.(prSelector).mode != modeSending {
		t.Fatalf("mode = %v, want sending", m.(prSelector).mode)
	}
	m, _ = m.Update(cmd())
	return m
}

func TestPRCommentInlineOnAddedLine(t *testing.T) {
	p := &poster{}
	m := openPreview(t, sampleDiff, prActions{postComment: p.post}, 100, 20)
	m, _ = keyPress(m, 'G') // the last row is "+added line" (new line 2 of x.go)

	m, _ = keyPress(m, 'c')
	sel := m.(prSelector)
	if sel.mode != modeComment {
		t.Fatalf("c should open the comment box (mode %v, notice %q)", sel.mode, sel.notice)
	}
	view := plain(sel.View().Content)
	for _, want := range []string{"Comment on x.go:2 (new)", "added line", "ctrl+s post"} {
		if !strings.Contains(view, want) {
			t.Errorf("comment screen missing %q:\n%s", want, view)
		}
	}

	m = submit(t, feed(m, "Nice change"))
	if len(p.posted) != 1 {
		t.Fatalf("posted %d comments", len(p.posted))
	}
	if got, want := p.posted[0], (newComment{Body: "Nice change", Path: "x.go", To: 2}); got != want {
		t.Errorf("posted %+v, want %+v", got, want)
	}

	sel = m.(prSelector)
	if sel.mode != modeDiff || !sel.noticeOK || !strings.Contains(sel.notice, "x.go:2 (new)") {
		t.Errorf("after posting: mode %v, notice %q (ok %v)", sel.mode, sel.notice, sel.noticeOK)
	}
	if !strings.Contains(plain(sel.View().Content), "Comment posted") {
		t.Error("the diff preview should confirm the comment")
	}
}

func TestPRCommentInlineOnRemovedLine(t *testing.T) {
	p := &poster{}
	m := openPreview(t, sampleDiff, prActions{postComment: p.post}, 100, 20)
	m, _ = keyPress(m, 'G')
	m, _ = keyPress(m, 'k') // "-removed line" (old line 2)

	m, _ = keyPress(m, 'c')
	m = submit(t, feed(m, "Why drop this?"))
	if got, want := p.posted[0], (newComment{Body: "Why drop this?", Path: "x.go", From: 2}); got != want {
		t.Errorf("posted %+v, want %+v", got, want)
	}
}

func TestPRCommentSplitViewSideChoosesOldOrNew(t *testing.T) {
	p := &poster{}
	m := openPreview(t, sampleDiff, prActions{postComment: p.post}, 140, 20)
	m = feed(m, "s")
	m, _ = keyPress(m, 'G') // the paired row: removed line left, added line right

	m = feed(m, "h") // old side
	m, _ = keyPress(m, 'c')
	m = submit(t, feed(m, "old"))

	m = feed(m, "l") // new side
	m, _ = keyPress(m, 'c')
	submit(t, feed(m, "new"))

	want := []newComment{{Body: "old", Path: "x.go", From: 2}, {Body: "new", Path: "x.go", To: 2}}
	if len(p.posted) != 2 || p.posted[0] != want[0] || p.posted[1] != want[1] {
		t.Errorf("posted %+v, want %+v", p.posted, want)
	}
}

func TestPRCommentOnWholePullRequestFromDiff(t *testing.T) {
	p := &poster{}
	m := openPreview(t, sampleDiff, prActions{postComment: p.post}, 100, 20)

	m, _ = keyPress(m, 'C')
	if !strings.Contains(plain(m.(prSelector).View().Content), "Comment on #1 First") {
		t.Errorf("comment screen:\n%s", plain(m.(prSelector).View().Content))
	}
	m = submit(t, feed(m, "LGTM"))

	if got, want := p.posted[0], (newComment{Body: "LGTM"}); got != want {
		t.Errorf("posted %+v, want a general comment %+v", got, want)
	}
	if m.(prSelector).mode != modeDiff || !strings.Contains(m.(prSelector).notice, "#1") {
		t.Errorf("mode %v, notice %q", m.(prSelector).mode, m.(prSelector).notice)
	}
}

func TestPRCommentFromList(t *testing.T) {
	p := &poster{}
	var m tea.Model = newPRSelector([]PullRequest{{ID: 1, Title: "First"}, {ID: 2, Title: "Second"}}, prActions{postComment: p.post})
	m, _ = keyPress(m, tea.KeyDown)
	m, _ = keyPress(m, 'c')
	m = submit(t, feed(m, "ping"))

	if len(p.posted) != 1 || p.posted[0].Path != "" || p.posted[0].Body != "ping" {
		t.Errorf("posted %+v", p.posted)
	}
	if sel := m.(prSelector); sel.mode != modeList || !strings.Contains(sel.notice, "#2") {
		t.Errorf("should be back on the list noting #2: mode %v, notice %q", sel.mode, sel.notice)
	}
}

func TestPRCommentOnNonCodeLineIsRefused(t *testing.T) {
	p := &poster{}
	m := openPreview(t, sampleDiff, prActions{postComment: p.post}, 100, 20) // cursor starts on the file header
	m, cmd := keyPress(m, 'c')

	sel := m.(prSelector)
	if sel.mode != modeDiff || cmd != nil || sel.noticeOK || !strings.Contains(sel.notice, "C comments on the pull request") {
		t.Errorf("mode %v, notice %q", sel.mode, sel.notice)
	}
}

func TestPRCommentFailureKeepsTheText(t *testing.T) {
	p := &poster{fail: 1}
	m := openPreview(t, sampleDiff, prActions{postComment: p.post}, 100, 20)
	m, _ = keyPress(m, 'C')
	m = submit(t, feed(m, "important thoughts"))

	sel := m.(prSelector)
	if sel.mode != modeComment || !strings.Contains(sel.comment.err, "503") || len(p.posted) != 0 {
		t.Fatalf("mode %v, err %q, posted %d", sel.mode, sel.comment.err, len(p.posted))
	}
	if !strings.Contains(plain(sel.View().Content), "Couldn't post the comment") || sel.comment.input.Value() != "important thoughts" {
		t.Errorf("the error should show and the text survive:\n%s", plain(sel.View().Content))
	}

	m = submit(t, m) // retry without retyping
	if len(p.posted) != 1 || p.posted[0].Body != "important thoughts" {
		t.Errorf("retry posted %+v", p.posted)
	}
}

func TestPRCommentEmptyIsRejected(t *testing.T) {
	p := &poster{}
	m := openPreview(t, sampleDiff, prActions{postComment: p.post}, 100, 20)
	m, _ = keyPress(m, 'C')
	m = feed(m, "   ")
	m, cmd := m.Update(ctrlS)

	if cmd != nil || m.(prSelector).mode != modeComment || !strings.Contains(m.(prSelector).comment.err, "Write a comment") {
		t.Errorf("empty comments must not be posted (mode %v, err %q)", m.(prSelector).mode, m.(prSelector).comment.err)
	}
	if len(p.posted) != 0 {
		t.Error("nothing should have been posted")
	}
}

func TestPRCommentDiscard(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEscape}, {Code: 'c', Mod: tea.ModCtrl}} {
		p := &poster{}
		m := openPreview(t, sampleDiff, prActions{postComment: p.post}, 100, 20)
		m, _ = keyPress(m, 'C')
		m = feed(m, "never mind")

		var cmd tea.Cmd
		m, cmd = m.Update(key)
		if cmd != nil || m.(prSelector).mode != modeDiff || len(p.posted) != 0 {
			t.Errorf("%v should discard back to the diff without quitting or posting (mode %v)", key, m.(prSelector).mode)
		}
	}
}

func TestPRCommentTypingIsNotInterpretedAsShortcuts(t *testing.T) {
	m := openPreview(t, sampleDiff, prActions{postComment: (&poster{}).post}, 100, 20)
	m, _ = keyPress(m, 'C')
	m = feed(m, "quit sCvj") // q, s, C, v, j are all shortcuts elsewhere

	sel := m.(prSelector)
	if sel.mode != modeComment || sel.comment.input.Value() != "quit sCvj" {
		t.Errorf("mode %v, text %q", sel.mode, sel.comment.input.Value())
	}
}

func TestPRCommentLateResultIsIgnored(t *testing.T) {
	m := openPreview(t, sampleDiff, prActions{postComment: (&poster{}).post}, 100, 20)
	m, _ = m.Update(prCommentMsg{id: 1}) // nothing is being posted
	if m.(prSelector).notice != "" || m.(prSelector).mode != modeDiff {
		t.Error("an unexpected result must be ignored")
	}
}

func TestCreateComment(t *testing.T) {
	var got []map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		switch r.URL.Path {
		case "/repositories/ws/foo/pullrequests/7/comments":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			got = append(got, body)
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"id":1}`)
		case "/repositories/ws/foo/pullrequests/8/comments":
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"type":"error","error":{"message":"Bad request","fields":{"inline":["line out of range"]}}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	old := bitbucketAPI
	bitbucketAPI = srv.URL
	defer func() { bitbucketAPI = old }()
	cfg := &Config{Workspace: "ws", Email: "e", Token: "t"}

	if err := createComment(t.Context(), cfg, "foo", 7, newComment{Body: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := createComment(t.Context(), cfg, "foo", 7, newComment{Body: "here", Path: "dir/x.go", To: 12}); err != nil {
		t.Fatal(err)
	}
	if err := createComment(t.Context(), cfg, "foo", 7, newComment{Body: "gone", Path: "x.go", From: 3}); err != nil {
		t.Fatal(err)
	}

	general, onNew, onOld := got[0], got[1], got[2]
	if general["content"].(map[string]any)["raw"] != "hello" {
		t.Errorf("general = %v", general)
	}
	if _, ok := general["inline"]; ok {
		t.Errorf("a general comment must not carry inline: %v", general)
	}
	if in := onNew["inline"].(map[string]any); in["path"] != "dir/x.go" || in["to"] != 12.0 || in["from"] != nil {
		t.Errorf("new-side inline = %v", in)
	}
	if in := onOld["inline"].(map[string]any); in["path"] != "x.go" || in["from"] != 3.0 || in["to"] != nil {
		t.Errorf("old-side inline = %v", in)
	}

	err := createComment(t.Context(), cfg, "foo", 8, newComment{Body: "x", Path: "f", To: 999})
	if err == nil || !strings.Contains(err.Error(), "line out of range") {
		t.Errorf("want Bitbucket's field message, got %v", err)
	}
	if err := createComment(t.Context(), cfg, "foo", 99, newComment{Body: "x"}); err == nil || !strings.Contains(err.Error(), "#99") {
		t.Errorf("missing PR: got %v (last path %s)", err, path)
	}
}
