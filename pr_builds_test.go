package bb

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func status(key, state string, minute int) BuildStatus {
	return BuildStatus{Key: key, State: state, UpdatedOn: time.Date(2026, 1, 1, 0, minute, 0, 0, time.UTC)}
}

func TestCountBuilds(t *testing.T) {
	got := countBuilds([]BuildStatus{
		status("a", "SUCCESSFUL", 1),
		status("b", "SUCCESSFUL", 1),
		status("c", "failed", 1), // case doesn't matter
		status("d", "INPROGRESS", 1),
		status("e", "STOPPED", 1), // none of the three
		status("f", "FAILED", 1),  // reported again, newer, as passing: counts once, as passing
		status("f", "SUCCESSFUL", 2),
		status("g", "SUCCESSFUL", 5), // …and the other way round
		status("g", "FAILED", 3),
		{State: "FAILED"}, // no key: can't be matched up, so each counts
		{State: "FAILED"},
	})
	if want := (buildCounts{success: 4, failed: 3, running: 1}); got != want {
		t.Errorf("counts = %+v, want %+v", got, want)
	}
	if (buildCounts{}).total() != 0 || got.total() != 8 {
		t.Errorf("totals wrong: %d", got.total())
	}
}

func TestBuildCountsRender(t *testing.T) {
	if got := (buildCounts{}).render(); got != "" {
		t.Errorf("no builds should render nothing, got %q", got)
	}
	if got := plain(buildCounts{success: 3, failed: 1, running: 2}.render()); got != "Builds: 3 success, 1 failed, 2 in progress" {
		t.Errorf("all states = %q", got)
	}
	if got := plain(buildCounts{success: 5}.render()); got != "Builds: 5 success" {
		t.Errorf("only passing = %q", got)
	}
	if got := plain(buildCounts{failed: 2, running: 7}.render()); got != "Builds: 2 failed, 7 in progress" {
		t.Errorf("states without builds are left out, got %q", got)
	}

	// Each count is colored as a whole, with its name.
	raw := buildCounts{success: 3, failed: 1, running: 2}.render()
	for _, want := range []string{diffAddStyle.Render("3 success"), diffDelStyle.Render("1 failed"), buildRunningStyle.Render("2 in progress")} {
		if !strings.Contains(raw, want) {
			t.Errorf("%q missing its color: %q", plain(want), raw)
		}
	}
}

func TestListBuildStatuses(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repositories/ws/foo/pullrequests/7/statuses" && r.URL.Query().Get("page") == "2":
			io.WriteString(w, `{"values":[{"key":"b","state":"FAILED","updated_on":"2026-01-01T00:02:00Z"}]}`)
		case r.URL.Path == "/repositories/ws/foo/pullrequests/7/statuses":
			fmt.Fprintf(w, `{"values":[{"key":"a","state":"SUCCESSFUL","updated_on":"2026-01-01T00:01:00Z"}],"next":"%s%s?page=2"}`, srv.URL, r.URL.Path)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	old := bitbucketAPI
	bitbucketAPI = srv.URL
	defer func() { bitbucketAPI = old }()
	cfg := &Config{Workspace: "ws", Email: "e", Token: "t"}

	got, err := listBuildStatuses(t.Context(), cfg, "foo", 7)
	if err != nil || len(got) != 2 || got[0].State != "SUCCESSFUL" || got[1].Key != "b" {
		t.Errorf("statuses = %+v, err %v", got, err)
	}
	if c := countBuilds(got); c.success != 1 || c.failed != 1 {
		t.Errorf("counts = %+v", c)
	}
	if _, err := listBuildStatuses(t.Context(), cfg, "foo", 99); err == nil || !strings.Contains(err.Error(), "#99") {
		t.Errorf("missing PR: %v", err)
	}
}

// openWithBuilds opens the preview of pull request #1 (of two) with builds available.
func openWithBuilds(t *testing.T, load func(PullRequest) ([]BuildStatus, error)) tea.Model {
	t.Helper()
	var m tea.Model = newPRSelector([]PullRequest{{ID: 1, Title: "First"}, {ID: 2, Title: "Second"}}, prActions{
		loadDiff:   func(PullRequest) (string, bool, error) { return sampleDiff, false, nil },
		loadBuilds: load,
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m, cmd := keyPress(m, tea.KeyEnter)
	m = deliver(m, cmd)
	if m.(prSelector).mode != modeDiff {
		t.Fatalf("preview didn't open (mode %v)", m.(prSelector).mode)
	}
	return m
}

func titleLine(m tea.Model) string {
	return strings.SplitN(m.(prSelector).View().Content, "\n", 2)[0]
}

func TestPRTitleBarShowsBuildCounts(t *testing.T) {
	m := openWithBuilds(t, func(PullRequest) ([]BuildStatus, error) {
		return []BuildStatus{
			status("a", "SUCCESSFUL", 1), status("b", "SUCCESSFUL", 1), status("c", "SUCCESSFUL", 1),
			status("d", "FAILED", 1),
			status("e", "INPROGRESS", 1), status("f", "INPROGRESS", 1),
		}, nil
	})

	raw := titleLine(m)
	title := plain(raw)
	if !strings.HasPrefix(title, "#1 First") || !strings.HasSuffix(title, "unified  Builds: 3 success, 1 failed, 2 in progress") {
		t.Errorf("title = %q, want the named build counts at the end", title)
	}
	for _, want := range []string{diffAddStyle.Render("3 success"), diffDelStyle.Render("1 failed"), buildRunningStyle.Render("2 in progress")} {
		if !strings.Contains(raw, want) {
			t.Errorf("%q should be colored: %q", plain(want), raw)
		}
	}
}

func TestPRTitleBarWithoutBuilds(t *testing.T) {
	for name, load := range map[string]func(PullRequest) ([]BuildStatus, error){
		"none reported": func(PullRequest) ([]BuildStatus, error) { return nil, nil },
		"only stopped":  func(PullRequest) ([]BuildStatus, error) { return []BuildStatus{status("a", "STOPPED", 1)}, nil },
		"no loader":     nil,
	} {
		m := openWithBuilds(t, load)
		if title := plain(titleLine(m)); !strings.HasSuffix(title, "unified") {
			t.Errorf("%s: nothing should be added to the title, got %q", name, title)
		}
	}
}

func TestPRBuildsFailureIsANoticeNotAnObstacle(t *testing.T) {
	m := openWithBuilds(t, func(PullRequest) ([]BuildStatus, error) { return nil, fmt.Errorf("403 scope") })

	sel := m.(prSelector)
	if sel.mode != modeDiff || sel.noticeOK || !strings.Contains(sel.notice, "Couldn't load the builds of #1: 403 scope") {
		t.Errorf("mode %v, notice %q", sel.mode, sel.notice)
	}
	if strings.Contains(plain(titleLine(m)), "403") {
		t.Error("the error belongs in the notice, not the title")
	}
}

func TestPRBuildsAreResetBetweenPullRequests(t *testing.T) {
	m := openWithBuilds(t, func(pr PullRequest) ([]BuildStatus, error) {
		if pr.ID == 1 {
			return []BuildStatus{status("a", "FAILED", 1)}, nil
		}
		return nil, fmt.Errorf("#2's builds can't be loaded") // so nothing overwrites a leftover count
	})
	if !strings.HasSuffix(plain(titleLine(m)), "unified  Builds: 1 failed") {
		t.Fatalf("title = %q", plain(titleLine(m)))
	}

	m, _ = keyPress(m, 'q')
	m, _ = keyPress(m, tea.KeyDown)
	m, cmd := keyPress(m, tea.KeyEnter)
	m = deliver(m, cmd)
	if title := plain(titleLine(m)); !strings.HasPrefix(title, "#2") || !strings.HasSuffix(title, "unified") {
		t.Errorf("#1's builds leaked into #2's title: %q", title)
	}
}

func TestPRBuildsIgnoreStaleResults(t *testing.T) {
	var m tea.Model = newPRSelector([]PullRequest{{ID: 1}}, prActions{
		loadDiff:   func(PullRequest) (string, bool, error) { return sampleDiff, false, nil },
		loadBuilds: func(PullRequest) ([]BuildStatus, error) { return []BuildStatus{status("a", "FAILED", 1)}, nil },
	})
	m, cmd := keyPress(m, tea.KeyEnter)
	m, _ = keyPress(m, 'q') // back out while loading
	m = deliver(m, cmd)
	if m.(prSelector).builds.total() != 0 {
		t.Error("builds for a screen the user left must be ignored")
	}

	m = openWithBuilds(t, func(PullRequest) ([]BuildStatus, error) { return nil, nil })
	m, _ = m.Update(prBuildsMsg{id: 2, statuses: []BuildStatus{status("a", "FAILED", 1)}})
	if m.(prSelector).builds.total() != 0 {
		t.Error("builds of another pull request must be ignored")
	}
}

func TestPRTitleBarKeepsBuildCountsWhenTheTitleIsLong(t *testing.T) {
	var m tea.Model = newPRSelector([]PullRequest{{ID: 12, Title: strings.Repeat("very long title ", 10)}}, prActions{
		loadDiff: func(PullRequest) (string, bool, error) { return sampleDiff, false, nil },
		loadBuilds: func(PullRequest) ([]BuildStatus, error) {
			return []BuildStatus{status("a", "SUCCESSFUL", 1), status("b", "FAILED", 1), status("c", "INPROGRESS", 1)}, nil
		},
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m, cmd := keyPress(m, tea.KeyEnter)
	m = deliver(m, cmd)

	title := plain(titleLine(m))
	if !strings.HasSuffix(title, "Builds: 1 success, 1 failed, 1 in progress") || !strings.Contains(title, "…") || lipglossWidth(title) > 80 {
		t.Errorf("the title should shrink, not the build counts (%d wide): %q", lipglossWidth(title), title)
	}
}

func lipglossWidth(s string) int { return ansi.StringWidth(s) }
