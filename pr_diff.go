package bb

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// prMode says which screen the pull request selector is showing.
type prMode int

const (
	modeList prMode = iota
	modeLoadingDiff
	modeDiff
)

// diffChrome is the number of screen lines the diff preview uses besides the diff itself:
// a title line and a help line.
const diffChrome = 2

// prDiffMsg delivers the result of fetching a pull request's diff.
type prDiffMsg struct {
	id        int
	diff      string
	truncated bool
	err       error
}

var (
	diffAddStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	diffDelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	diffHunkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("37"))
	diffMetaStyle = lipgloss.NewStyle().Bold(true)
)

// colorizeDiff styles a unified diff line by line: additions green, deletions red, hunk headers
// cyan, and file headers bold. Tabs become spaces so the viewport's width math stays right.
func colorizeDiff(diff string) string {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(diff, "\r", ""), "\t", "    "), "\n")
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "diff "), strings.HasPrefix(l, "index "),
			strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
			lines[i] = diffMetaStyle.Render(l)
		case strings.HasPrefix(l, "@@"):
			lines[i] = diffHunkStyle.Render(l)
		case strings.HasPrefix(l, "+"):
			lines[i] = diffAddStyle.Render(l)
		case strings.HasPrefix(l, "-"):
			lines[i] = diffDelStyle.Render(l)
		}
	}
	return strings.Join(lines, "\n")
}

// fetchDiff returns a command that loads pr's diff in the background.
func (m prSelector) fetchDiff(pr PullRequest) tea.Cmd {
	load := m.loadDiff
	return func() tea.Msg {
		diff, truncated, err := load(pr)
		return prDiffMsg{id: pr.ID, diff: diff, truncated: truncated, err: err}
	}
}

// diffLoaded handles a finished fetch, ignoring results the user has already navigated away from.
func (m prSelector) diffLoaded(msg prDiffMsg) prSelector {
	if m.mode != modeLoadingDiff || msg.id != m.diffPR.ID {
		return m
	}
	if msg.err != nil {
		m.mode = modeList
		m.notice = fmt.Sprintf("Couldn't load the diff of #%d: %v", msg.id, msg.err)
		return m
	}

	diff := msg.diff
	if strings.TrimSpace(diff) == "" {
		diff = "(this pull request has no changes)"
	}
	m.diff = viewport.New(viewport.WithWidth(m.width), viewport.WithHeight(max(1, m.height-diffChrome)))
	m.diff.SetContent(colorizeDiff(diff))
	m.diffNote = ""
	if msg.truncated {
		m.diffNote = fmt.Sprintf("diff truncated at %d MiB • ", maxDiffBytes>>20)
	}
	m.mode = modeDiff
	return m
}

// updateDiff handles a key press while the diff preview is showing.
func (m prSelector) updateDiff(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "backspace":
		m.mode = modeList
		return m, nil
	case "g", "home":
		m.diff.GotoTop()
		return m, nil
	case "G", "end":
		m.diff.GotoBottom()
		return m, nil
	}
	var cmd tea.Cmd
	m.diff, cmd = m.diff.Update(msg)
	return m, cmd
}

func (m prSelector) diffView() string {
	title := titleStyle.Render(fmt.Sprintf("#%d %s", m.diffPR.ID, truncate(m.diffPR.Title, max(10, m.width-20))))
	title += dimStyle.Render(fmt.Sprintf("  %3.0f%%", m.diff.ScrollPercent()*100))
	help := dimStyle.Render(m.diffNote + "↑/↓ j/k scroll • space/b page • g/G top/bottom • ←/→ pan • q back")
	return title + "\n" + m.diff.View() + "\n" + help
}
