package bb

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Comments on sampleDiff: a thread with a reply on the added line (x.go new line 2), a comment on
// the removed line (old line 2), and a general comment.
const sampleComments = `[
 {"id":4,"content":{"raw":"Overall fine"},"user":{"display_name":"Cy"},"created_on":"2026-01-02T12:00:00Z"},
 {"id":2,"content":{"raw":"Because."},"user":{"display_name":"Bob"},"created_on":"2026-01-02T10:05:00Z","parent":{"id":1}},
 {"id":1,"content":{"raw":"Why this?"},"user":{"display_name":"Ann"},"created_on":"2026-01-02T10:00:00Z","inline":{"path":"x.go","from":null,"to":2}},
 {"id":3,"content":{"raw":"Was needed"},"user":{"display_name":"Ann"},"created_on":"2026-01-02T11:00:00Z","inline":{"path":"x.go","from":2,"to":null}}
]`

func mustComments(t *testing.T, raw string) []Comment {
	t.Helper()
	var cs []Comment
	if err := json.Unmarshal([]byte(raw), &cs); err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestBuildThreads(t *testing.T) {
	threads := buildThreads(mustComments(t, sampleComments))

	var ids [][]int
	for _, th := range threads {
		row := []int{th.root.ID}
		for _, r := range th.replies {
			row = append(row, r.comment.ID)
		}
		ids = append(ids, row)
	}
	// Oldest thread first, the reply beneath its parent.
	if fmt.Sprint(ids) != "[[1 2] [3] [4]]" {
		t.Errorf("threads = %v", ids)
	}
	if threads[0].replies[0].depth != 1 {
		t.Errorf("reply depth = %d", threads[0].replies[0].depth)
	}
	if got := countComments(threads); got != 4 {
		t.Errorf("count = %d", got)
	}
}

func TestBuildThreadsNestedOrphanAndDeleted(t *testing.T) {
	threads := buildThreads(mustComments(t, `[
	 {"id":1,"content":{"raw":"root"},"created_on":"2026-01-01T00:00:00Z"},
	 {"id":2,"content":{"raw":"r1"},"created_on":"2026-01-01T00:01:00Z","parent":{"id":1}},
	 {"id":3,"content":{"raw":"r2"},"created_on":"2026-01-01T00:02:00Z","parent":{"id":2}},
	 {"id":4,"content":{"raw":"orphan"},"created_on":"2026-01-01T00:03:00Z","parent":{"id":999}},
	 {"id":5,"content":{"raw":""},"deleted":true,"created_on":"2026-01-01T00:04:00Z"},
	 {"id":6,"content":{"raw":""},"deleted":true,"created_on":"2026-01-01T00:05:00Z"},
	 {"id":7,"content":{"raw":"kept reply"},"created_on":"2026-01-01T00:06:00Z","parent":{"id":6}},
	 {"id":8,"content":{"raw":"self"},"created_on":"2026-01-01T00:07:00Z","parent":{"id":8}}
	]`))

	var roots []int
	for _, th := range threads {
		roots = append(roots, th.root.ID)
	}
	if !slices.Equal(roots, []int{1, 4, 6, 8}) {
		t.Errorf("roots = %v; want orphan and self-parented comments as roots, an unanswered deleted one dropped, an answered one kept", roots)
	}
	if r := threads[0].replies; len(r) != 2 || r[0].depth != 1 || r[1].depth != 2 || r[1].comment.ID != 3 {
		t.Errorf("nested replies = %+v", r)
	}
	if got := countComments(threads); got != 6 { // 1,2,3,4,7,8 (the deleted parent 6 doesn't count)
		t.Errorf("count = %d, want 6", got)
	}
}

func TestThreadsByLine(t *testing.T) {
	lines := parseDiff("diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -2,3 +2,3 @@\n same\n-gone\n+new\n tail\n" +
		"diff --git a/y.go b/y.go\n--- a/y.go\n+++ b/y.go\n@@ -3 +3 @@\n-old\n+fresh\n")
	threads := buildThreads(mustComments(t, `[
	 {"id":1,"content":{"raw":"a"},"created_on":"2026-01-01T00:00:00Z","inline":{"path":"x.go","to":3}},
	 {"id":2,"content":{"raw":"b"},"created_on":"2026-01-01T00:01:00Z","inline":{"path":"x.go","from":3}},
	 {"id":3,"content":{"raw":"c"},"created_on":"2026-01-01T00:02:00Z","inline":{"path":"y.go","to":3}},
	 {"id":4,"content":{"raw":"d"},"created_on":"2026-01-01T00:03:00Z","inline":{"path":"x.go","to":2}},
	 {"id":5,"content":{"raw":"general"},"created_on":"2026-01-01T00:04:00Z"},
	 {"id":6,"content":{"raw":"nowhere"},"created_on":"2026-01-01T00:05:00Z","inline":{"path":"gone.go","to":1}}
	]`))

	got := map[string][]int{}
	for i, ts := range threadsByLine(lines, threads) {
		for _, ti := range ts {
			got[lines[i].kind.String()+":"+lines[i].text] = append(got[lines[i].kind.String()+":"+lines[i].text], threads[ti].root.ID)
		}
	}
	want := map[string][]int{
		"add:new":      {1}, // x.go new line 3; the removal that was old line 3 doesn't collect it
		"del:gone":     {2}, // x.go old line 3
		"context:same": {4}, // x.go new line 2
		"add:fresh":    {3}, // y.go new line 3, not x.go's
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

func (k diffKind) String() string {
	return [...]string{"meta", "file", "hunk", "context", "add", "del"}[k]
}

func TestRenderThreads(t *testing.T) {
	long := strings.Repeat("word ", 30) + strings.Repeat("x", 80) // plus an unbreakable run
	threads := buildThreads(mustComments(t, `[
	 {"id":1,"content":{"raw":"first line\n\nsecond\u001b[2J paragraph `+long+`"},"user":{"display_name":"Ann\u001b[31m"},"created_on":"2026-01-02T10:00:00Z","inline":{"path":"x.go","to":2}},
	 {"id":2,"content":{"raw":"reply"},"user":{"display_name":"Bob"},"created_on":"2026-01-02T10:05:00Z","parent":{"id":1}},
	 {"id":3,"content":{"raw":""},"deleted":true,"user":{"display_name":"Zed"},"created_on":"2026-01-02T10:06:00Z","parent":{"id":1}},
	 {"id":4,"content":{"raw":"later"},"pending":true,"created_on":"2026-01-03T10:00:00Z"}
	]`))

	const width = 50
	lines := renderThreads(threads, width)
	text := ansi.Strip(strings.Join(lines, "\n"))

	for i, l := range lines {
		if w := ansi.StringWidth(l); w > width {
			t.Errorf("line %d is %d wide (max %d): %q", i, w, width, ansi.Strip(l))
		}
	}
	for _, want := range []string{"x.go:2 (new)", "Ann", "first line", "↳ Bob", "(deleted)", "unknown", "(pending)"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendering missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(strings.Join(lines, "\n"), "\x1b[2J") || strings.Contains(text, "\x1b") {
		t.Error("terminal escape sequences from comment text must not survive")
	}
	if !strings.Contains(text, "xxxxxxxx") || strings.Count(text, "x") < 80 {
		t.Error("long words should be wrapped, not dropped")
	}
}

func TestThreadViewScrollsAndResizes(t *testing.T) {
	body := strings.Repeat("line\n", 40)
	threads := buildThreads(mustComments(t, `[{"id":1,"content":{"raw":`+fmt.Sprintf("%q", body)+`},"created_on":"2026-01-01T00:00:00Z"}]`))
	v := newThreadView("T", threads, 40, 5)

	if v.offset != 0 || strings.Count(v.view(), "\n")+1 != 5 {
		t.Fatalf("offset %d, view height %d", v.offset, strings.Count(v.view(), "\n")+1)
	}
	v.handleKey("G")
	if v.offset != v.maxOffset() || v.percent() != 100 {
		t.Errorf("G: offset %d of %d", v.offset, v.maxOffset())
	}
	v.handleKey("j")
	if v.offset != v.maxOffset() {
		t.Error("scrolling must stop at the end")
	}
	v.handleKey("pgup")
	if v.offset != v.maxOffset()-5 {
		t.Errorf("pgup: offset %d", v.offset)
	}
	v.handleKey("g")
	v.handleKey("k")
	if v.offset != 0 {
		t.Error("scrolling must stop at the top")
	}
	v.handleKey("G")
	v.resize(40, 100) // taller than the content: offset must clamp
	if v.offset != 0 {
		t.Errorf("offset after growing = %d", v.offset)
	}
}

func TestListComments(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repositories/ws/foo/pullrequests/7/comments" && r.URL.Query().Get("page") == "2":
			io.WriteString(w, `{"values":[{"id":2,"content":{"raw":"b"}}]}`)
		case r.URL.Path == "/repositories/ws/foo/pullrequests/7/comments":
			fmt.Fprintf(w, `{"values":[{"id":1,"content":{"raw":"a"},"inline":{"path":"f","to":3}}],"next":"%s%s?page=2"}`, srv.URL, r.URL.Path)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	old := bitbucketAPI
	bitbucketAPI = srv.URL
	defer func() { bitbucketAPI = old }()
	cfg := &Config{Workspace: "ws", Email: "e", Token: "t"}

	cs, err := listComments(t.Context(), cfg, "foo", 7)
	if err != nil || len(cs) != 2 || cs[0].Inline == nil || *cs[0].Inline.To != 3 || cs[1].Content.Raw != "b" {
		t.Errorf("comments = %+v, err %v", cs, err)
	}
	if _, err := listComments(t.Context(), cfg, "foo", 99); err == nil || !strings.Contains(err.Error(), "#99") {
		t.Errorf("missing PR: %v", err)
	}
}

// ---- the diff viewer and the selector

func TestDiffViewerMarksCommentedLines(t *testing.T) {
	threads := buildThreads(mustComments(t, sampleComments))
	for _, width := range []int{80, 140} { // unified, then split
		v := newDiffViewer(sampleDiff, width, 20, width > 100)
		if strings.Contains(v.view(), "●") {
			t.Fatal("nothing should be marked before comments are set")
		}
		v.setThreads(threads)

		marked := 0
		for row := range strings.SplitSeq(plain(v.view()), "\n") {
			if strings.Contains(row, "●") {
				marked++
			}
		}
		want := 2 // the removed line and the added line, separate rows in unified…
		if v.rowsSplit {
			want = 1 // …one paired row in split
		}
		if marked != want {
			t.Errorf("width %d (split %v): %d marked rows, want %d:\n%s", width, v.rowsSplit, marked, want, plain(v.view()))
		}
	}
}

func TestDiffViewerJumpBetweenComments(t *testing.T) {
	v := newDiffViewer(sampleDiff, 80, 20, false)
	v.setThreads(buildThreads(mustComments(t, sampleComments)))

	if !v.jump(1) || v.lines[v.cursorLine()].kind != diffDel {
		t.Fatalf("first jump should land on the removed line, got %+v", v.lines[v.cursorLine()])
	}
	if !v.jump(1) || v.lines[v.cursorLine()].kind != diffAdd {
		t.Fatal("second jump should land on the added line")
	}
	if v.jump(1) {
		t.Error("no commented line after the last one")
	}
	if !v.jump(-1) || v.lines[v.cursorLine()].kind != diffDel {
		t.Error("jumping back should return to the removed line")
	}
}

func TestDiffViewerJumpSelectsTheCommentedSide(t *testing.T) {
	// Only the removed (left) line of the paired row has a comment.
	v := newDiffViewer(sampleDiff, 140, 20, true)
	v.setThreads(buildThreads(mustComments(t, `[{"id":3,"content":{"raw":"x"},"created_on":"2026-01-01T00:00:00Z","inline":{"path":"x.go","from":2}}]`)))

	if !v.jump(1) || v.side != 0 || v.lines[v.cursorLine()].kind != diffDel {
		t.Errorf("jump should put the cursor on the commented old side (side %d)", v.side)
	}
}

// deliver runs cmd and feeds what it produces back into the model, following batches.
func deliver(m tea.Model, cmd tea.Cmd) tea.Model {
	if cmd == nil {
		return m
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			m = deliver(m, c)
		}
		return m
	}
	m, next := m.Update(msg)
	return deliver(m, next)
}

type commentSource struct {
	calls    int
	comments []Comment
	err      error
}

func (s *commentSource) load(PullRequest) ([]Comment, error) {
	s.calls++
	return s.comments, s.err
}

func newSource(t *testing.T) *commentSource {
	return &commentSource{comments: mustComments(t, sampleComments)}
}

// openPreviewWithComments opens the preview of pull request #1 with comments available.
func openPreviewWithComments(t *testing.T, src *commentSource, extra prActions) tea.Model {
	t.Helper()
	extra.loadDiff = func(PullRequest) (string, bool, error) { return sampleDiff, false, nil }
	extra.loadComments = src.load
	var m tea.Model = newPRSelector([]PullRequest{{ID: 1, Title: "First"}, {ID: 2, Title: "Second"}}, extra)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m, cmd := keyPress(m, 'v')
	m = deliver(m, cmd)
	if m.(prSelector).mode != modeDiff {
		t.Fatalf("preview didn't open (mode %v, notice %q)", m.(prSelector).mode, m.(prSelector).notice)
	}
	return m
}

func TestPRSelectorShowsCommentsInPreview(t *testing.T) {
	src := newSource(t)
	m := openPreviewWithComments(t, src, prActions{})

	view := plain(m.(prSelector).View().Content)
	if !strings.Contains(view, "4 comments") || !strings.Contains(view, "●") {
		t.Errorf("title should count comments and the lines should be marked:\n%s", view)
	}
	if !strings.Contains(view, "n/N jump") || !strings.Contains(view, "enter/T read") {
		t.Errorf("help should mention the comment keys:\n%s", view)
	}

	// Jump to the first commented line and read it.
	m, _ = keyPress(m, 'n')
	m, _ = keyPress(m, tea.KeyEnter)
	sel := m.(prSelector)
	if sel.mode != modeThreads {
		t.Fatalf("enter on a commented line should open the comments (mode %v, notice %q)", sel.mode, sel.notice)
	}
	text := plain(sel.View().Content)
	for _, want := range []string{"Comments on x.go:2 (old)", "Was needed", "Ann"} {
		if !strings.Contains(text, want) {
			t.Errorf("comment reader missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Why this?") {
		t.Errorf("only the cursor line's comments should show:\n%s", text)
	}

	m, _ = keyPress(m, 'q') // back to the diff, not the list
	if m.(prSelector).mode != modeDiff {
		t.Errorf("q should return to the diff (mode %v)", m.(prSelector).mode)
	}
}

func TestPRSelectorReadsReplyThread(t *testing.T) {
	m := openPreviewWithComments(t, newSource(t), prActions{})
	m, _ = keyPress(m, 'n')
	m, _ = keyPress(m, 'n') // the added line, with a reply
	m, _ = keyPress(m, tea.KeyEnter)

	text := plain(m.(prSelector).View().Content)
	for _, want := range []string{"Why this?", "↳ Bob", "Because.", "2 comments"} {
		if !strings.Contains(text, want) {
			t.Errorf("thread missing %q:\n%s", want, text)
		}
	}
}

func TestPRSelectorReadsAllComments(t *testing.T) {
	m := openPreviewWithComments(t, newSource(t), prActions{})
	m = feed(m, "T")

	sel := m.(prSelector)
	text := plain(sel.View().Content)
	if sel.mode != modeThreads || !strings.Contains(text, "Comments on #1 First") {
		t.Fatalf("T should open all comments (mode %v):\n%s", sel.mode, text)
	}
	for _, want := range []string{"Why this?", "Was needed", "Overall fine", "x.go:2 (new)"} {
		if !strings.Contains(text, want) {
			t.Errorf("all-comments view missing %q:\n%s", want, text)
		}
	}
}

func TestPRSelectorNoCommentsOnLine(t *testing.T) {
	m := openPreviewWithComments(t, newSource(t), prActions{}) // the cursor starts on the file header
	m, _ = keyPress(m, tea.KeyEnter)

	sel := m.(prSelector)
	if sel.mode != modeDiff || !strings.Contains(sel.notice, "No comments on this line") {
		t.Errorf("mode %v, notice %q", sel.mode, sel.notice)
	}

	m, _ = keyPress(m, 'G')
	for range 3 {
		m = feed(m, "N")
	}
	if !strings.Contains(m.(prSelector).notice, "No more commented lines") {
		t.Errorf("jumping past the ends should say so, notice %q", m.(prSelector).notice)
	}
}

func TestPRSelectorMarkersAppearWhenCommentsArriveFirst(t *testing.T) {
	src := newSource(t)
	var m tea.Model = newPRSelector([]PullRequest{{ID: 1, Title: "First"}}, prActions{
		loadDiff:     func(PullRequest) (string, bool, error) { return sampleDiff, false, nil },
		loadComments: src.load,
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m, cmd := keyPress(m, 'v')

	batch := cmd().(tea.BatchMsg)
	var diffMsg, commentsMsg tea.Msg = batch[0](), batch[1]()
	m, _ = m.Update(commentsMsg) // comments win the race
	m, _ = m.Update(diffMsg)

	if len(m.(prSelector).viewer.commented) != 2 || !strings.Contains(plain(m.(prSelector).View().Content), "●") {
		t.Error("lines should be marked when the diff arrives after the comments")
	}
}

func TestPRSelectorDiffWorksWhenCommentsFail(t *testing.T) {
	src := &commentSource{err: fmt.Errorf("503 later")}
	m := openPreviewWithComments(t, src, prActions{})

	sel := m.(prSelector)
	if sel.mode != modeDiff || !strings.Contains(sel.notice, "Couldn't load the comments of #1: 503 later") {
		t.Errorf("mode %v, notice %q", sel.mode, sel.notice)
	}
	m, _ = keyPress(m, tea.KeyEnter)
	if !strings.Contains(m.(prSelector).notice, "still loading") {
		t.Errorf("reading without comments should say so, notice %q", m.(prSelector).notice)
	}
}

func TestPRSelectorReadsCommentsFromList(t *testing.T) {
	src := newSource(t)
	var m tea.Model = newPRSelector([]PullRequest{{ID: 1, Title: "First"}, {ID: 2, Title: "Second"}}, prActions{loadComments: src.load})
	if !strings.Contains(m.(prSelector).View().Content, "t read comments") {
		t.Error("list help should mention t")
	}

	m, cmd := keyPress(m, 't')
	if m.(prSelector).mode != modeLoadingComments || !strings.Contains(m.(prSelector).View().Content, "Loading the comments of #1") {
		t.Fatalf("mode %v", m.(prSelector).mode)
	}
	m = deliver(m, cmd)
	text := plain(m.(prSelector).View().Content)
	if m.(prSelector).mode != modeThreads || !strings.Contains(text, "Overall fine") {
		t.Fatalf("mode %v:\n%s", m.(prSelector).mode, text)
	}
	m, _ = keyPress(m, 'q')
	if m.(prSelector).mode != modeList {
		t.Errorf("q should return to the list, mode %v", m.(prSelector).mode)
	}
}

func TestPRSelectorListCommentEdgeCases(t *testing.T) {
	prs := []PullRequest{{ID: 1, Title: "First"}}

	// No comments yet.
	empty := &commentSource{}
	var m tea.Model = newPRSelector(prs, prActions{loadComments: empty.load})
	m, cmd := keyPress(m, 't')
	m = deliver(m, cmd)
	if sel := m.(prSelector); sel.mode != modeList || !strings.Contains(sel.notice, "No comments on #1") {
		t.Errorf("mode %v, notice %q", sel.mode, sel.notice)
	}

	// A failed load returns to the list with the reason.
	bad := &commentSource{err: fmt.Errorf("nope")}
	m = newPRSelector(prs, prActions{loadComments: bad.load})
	m, cmd = keyPress(m, 't')
	m = deliver(m, cmd)
	if sel := m.(prSelector); sel.mode != modeList || !strings.Contains(sel.notice, "nope") || sel.noticeOK {
		t.Errorf("mode %v, notice %q", sel.mode, sel.notice)
	}

	// Cancelling while loading ignores the late result.
	m = newPRSelector(prs, prActions{loadComments: newSource(t).load})
	m, cmd = keyPress(m, 't')
	m, _ = keyPress(m, 'q')
	m = deliver(m, cmd)
	if m.(prSelector).mode != modeList {
		t.Error("a late result must not reopen the comments")
	}

	// Without a loader the keys do nothing and aren't advertised.
	m = newPRSelector(prs, prActions{})
	m, cmd = keyPress(m, 't')
	if m.(prSelector).mode != modeList || cmd != nil || strings.Contains(m.(prSelector).View().Content, "read comments") {
		t.Error("t should be inert without loadComments")
	}
}

func TestPRSelectorRefreshesCommentsAfterPosting(t *testing.T) {
	src := newSource(t)
	p := &poster{}
	m := openPreviewWithComments(t, src, prActions{postComment: p.post})
	if src.calls != 1 {
		t.Fatalf("comments loaded %d times opening the preview", src.calls)
	}

	m, _ = keyPress(m, 'C')
	m = feed(m, "one more")
	m, cmd := m.Update(ctrlS)
	m, cmd = m.Update(cmd()) // posting finished; this returns the refetch
	if cmd == nil {
		t.Fatal("a successful post should refetch the comments")
	}
	src.comments = append(src.comments, mustComments(t, `[{"id":9,"content":{"raw":"one more"},"user":{"display_name":"Me"},"created_on":"2026-01-05T00:00:00Z"}]`)...)
	m = deliver(m, cmd)

	if src.calls != 2 || !strings.Contains(plain(m.(prSelector).View().Content), "5 comments") {
		t.Errorf("calls %d; the title should now count the new comment:\n%s", src.calls, plain(m.(prSelector).View().Content))
	}
}

func TestPRSelectorWithoutCommentLoaderHidesCommentKeys(t *testing.T) {
	m := openPreview(t, sampleDiff, prActions{}, 100, 20)
	view := plain(m.(prSelector).View().Content)
	if strings.Contains(view, "enter/T read") || strings.Contains(view, "comments") {
		t.Errorf("no comment UI without a loader:\n%s", view)
	}
	for _, k := range []string{"enter", "T", "n"} {
		var cmd tea.Cmd
		m, cmd = m.Update(tea.KeyPressMsg{Code: rune(k[0]), Text: k})
		if cmd != nil || m.(prSelector).mode != modeDiff {
			t.Errorf("%q should do nothing", k)
		}
	}
}

func TestPRSelectorIgnoresStaleComments(t *testing.T) {
	src := newSource(t)
	actions := prActions{
		loadDiff:     func(PullRequest) (string, bool, error) { return sampleDiff, false, nil },
		loadComments: src.load,
	}
	prs := []PullRequest{{ID: 1, Title: "First"}, {ID: 2, Title: "Second"}}

	// The user backs out of the preview before the comments arrive.
	var m tea.Model = newPRSelector(prs, actions)
	m, cmd := keyPress(m, 'v')
	m, _ = keyPress(m, 'q')
	m = deliver(m, cmd)
	if sel := m.(prSelector); sel.mode != modeList || sel.commentsReady || sel.threads != nil {
		t.Errorf("late comments leaked into the list screen: mode %v, ready %v", sel.mode, sel.commentsReady)
	}

	// Comments for a different pull request than the one on screen are ignored.
	m = openPreviewWithComments(t, &commentSource{}, prActions{})
	m, _ = m.Update(prCommentsMsg{id: 2, comments: src.comments})
	if sel := m.(prSelector); countComments(sel.threads) != 0 || strings.Contains(plain(sel.View().Content), "●") {
		t.Error("comments for another pull request must not be shown")
	}
}
