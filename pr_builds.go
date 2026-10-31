package bb

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var buildRunningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))

// buildCounts is how many of a pull request's builds are in each state. Stopped builds are in
// none of them.
type buildCounts struct{ success, failed, running int }

func (c buildCounts) total() int { return c.success + c.failed + c.running }

// countBuilds tallies statuses. A build that reported more than once (same key) counts once,
// by its most recent status.
func countBuilds(statuses []BuildStatus) buildCounts {
	latest := map[string]BuildStatus{}
	var unkeyed []BuildStatus
	for _, s := range statuses {
		if s.Key == "" {
			unkeyed = append(unkeyed, s)
		} else if prev, ok := latest[s.Key]; !ok || s.UpdatedOn.After(prev.UpdatedOn) {
			latest[s.Key] = s
		}
	}

	var c buildCounts
	tally := func(s BuildStatus) {
		switch strings.ToUpper(s.State) {
		case "SUCCESSFUL":
			c.success++
		case "FAILED":
			c.failed++
		case "INPROGRESS":
			c.running++
		}
	}
	for _, s := range latest {
		tally(s)
	}
	for _, s := range unkeyed {
		tally(s)
	}
	return c
}

// render is the title bar summary, e.g. "Builds: 3 success, 1 failed, 2 in progress": a count
// and name for each state that has builds, colored green, red and yellow. Empty if none.
func (c buildCounts) render() string {
	var parts []string
	for _, p := range []struct {
		n     int
		name  string
		style lipgloss.Style
	}{{c.success, "success", diffAddStyle}, {c.failed, "failed", diffDelStyle}, {c.running, "in progress", buildRunningStyle}} {
		if p.n > 0 {
			parts = append(parts, p.style.Render(fmt.Sprintf("%d %s", p.n, p.name)))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return dimStyle.Render("Builds: ") + strings.Join(parts, dimStyle.Render(", "))
}

// prBuildsMsg delivers the result of fetching a pull request's build statuses.
type prBuildsMsg struct {
	id       int
	statuses []BuildStatus
	err      error
}

// fetchBuilds returns a command that loads pr's build statuses in the background, or nil when
// they can't be loaded.
func (m prSelector) fetchBuilds(pr PullRequest) tea.Cmd {
	load := m.actions.loadBuilds
	if load == nil {
		return nil
	}
	return func() tea.Msg {
		statuses, err := load(pr)
		return prBuildsMsg{id: pr.ID, statuses: statuses, err: err}
	}
}

// buildsLoaded handles fetched build statuses, ignoring ones for a screen the user has left.
func (m prSelector) buildsLoaded(msg prBuildsMsg) prSelector {
	if msg.id != m.pr.ID || m.mode == modeList {
		return m
	}
	if msg.err != nil {
		m.notice, m.noticeOK = fmt.Sprintf("Couldn't load the builds of #%d: %v", msg.id, msg.err), false
		return m
	}
	m.builds = countBuilds(msg.statuses)
	return m
}

// buildsSummary is the diff title's builds note, with its leading gap, or "" without builds.
func (m prSelector) buildsSummary() string {
	if s := m.builds.render(); s != "" {
		return "  " + s
	}
	return ""
}
