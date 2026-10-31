package bb

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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

// fetchDiff returns a command that loads pr's diff in the background.
func (m prSelector) fetchDiff(pr PullRequest) tea.Cmd {
	load := m.actions.loadDiff
	return func() tea.Msg {
		diff, truncated, err := load(pr)
		return prDiffMsg{id: pr.ID, diff: diff, truncated: truncated, err: err}
	}
}

// diffLoaded handles a finished fetch, ignoring results the user has already navigated away from.
func (m prSelector) diffLoaded(msg prDiffMsg) prSelector {
	if m.mode != modeLoadingDiff || msg.id != m.pr.ID {
		return m
	}
	if msg.err != nil {
		m.mode = modeList
		m.notice, m.noticeOK = fmt.Sprintf("Couldn't load the diff of #%d: %v", msg.id, msg.err), false
		return m
	}

	m.viewer = newDiffViewer(msg.diff, m.width, m.height-diffChrome, m.split)
	if m.commentsReady {
		m.viewer.setThreads(m.threads)
	}
	m.diffNote = ""
	if msg.truncated {
		m.diffNote = fmt.Sprintf("diff truncated at %d MiB • ", maxDiffBytes>>20)
	}
	m.mode = modeDiff
	return m
}

// updateDiff handles a key press while the diff preview is showing.
func (m prSelector) updateDiff(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.notice = ""
	if m.confirmApprove { // y confirms; anything else (but ctrl+c) backs out
		m.confirmApprove = false
		if k := msg.String(); k == "y" || k == "Y" {
			m.mode = modeApproving
			return m, m.approve(m.pr)
		} else if k != "ctrl+c" {
			return m, nil
		}
	}

	switch key := msg.String(); key {
	case "ctrl+c":
		return m, tea.Quit
	case "w": // open in the browser
		pr := m.pr
		m.selected = &pr
		return m, tea.Quit
	case "a":
		if m.actions.approve != nil {
			m.confirmApprove = true
		}
	case "q", "esc", "backspace":
		m.mode = modeList
	case "s":
		m.split = !m.split
		m.viewer.wantSplit = m.split
		m.viewer.layout()
		if m.split && !m.viewer.rowsSplit {
			m.notice, m.noticeOK = fmt.Sprintf("Split view needs %d columns; using unified until the terminal is wider", m.viewer.minSplitWidth()), false
		}
	case "c":
		if m.actions.postComment == nil {
			return m, nil
		}
		_, ref, ok := m.viewer.commentLine()
		if !ok {
			m.notice, m.noticeOK = "Can't comment on this line: move to an added, removed or unchanged line (C comments on the pull request)", false
			return m, nil
		}
		return m.openComment(modeDiff, m.pr, &ref, inlineDescription(ref))
	case "C":
		if m.actions.postComment == nil {
			return m, nil
		}
		return m.openComment(modeDiff, m.pr, nil, "")
	case "enter":
		return m.readLineComments()
	case "T":
		return m.readAllComments(modeDiff)
	case "n", "N":
		if m.actions.loadComments == nil {
			return m, nil
		}
		if !m.commentsReady {
			m.notice, m.noticeOK = "Comments are still loading", false
		} else if !m.viewer.jump(map[string]int{"n": 1, "N": -1}[key]) {
			m.notice, m.noticeOK = "No more commented lines", false
		}
	default:
		m.viewer.handleKey(key)
	}
	return m, nil
}

// inlineDescription says where an inline comment will attach, e.g. "f.go:34 (new)".
func inlineDescription(ref inlineRef) string {
	if ref.to > 0 {
		return fmt.Sprintf("%s:%d (new)", ref.path, ref.to)
	}
	return fmt.Sprintf("%s:%d (old)", ref.path, ref.from)
}

func (m prSelector) diffView() string {
	layout := "unified"
	if m.viewer.rowsSplit {
		layout = "split"
	}
	// The title gives way to the status text on the right, so the build counts are never cut off.
	status := dimStyle.Render(fmt.Sprintf("  %d%%  %s%s", m.viewer.percent(), layout, m.commentsSummary())) + m.buildsSummary()
	id := fmt.Sprintf("#%d ", m.pr.ID)
	room := m.width - ansi.StringWidth(id) - ansi.StringWidth(status) - 1
	title := titleStyle.Render(id+truncate(m.pr.Title, max(10, room))) + status

	var footer string
	if m.confirmApprove {
		footer = titleStyle.Render(fmt.Sprintf("Approve #%d? Press y to confirm, any other key cancels.", m.pr.ID))
	} else if m.notice != "" {
		footer = m.noticeStyled()
	} else {
		footer = dimStyle.Render(m.diffNote + m.diffHelp())
	}
	return m.fitLine(title) + "\n" + m.viewer.view() + "\n" + m.fitLine(footer)
}

func (m prSelector) diffHelp() string {
	help := []string{"q back", "j/k move"}
	if m.viewer.rowsSplit {
		help = append(help, "←/→ side", "s unified")
	} else {
		help = append(help, "←/→ pan", "s split")
	}
	if m.actions.loadComments != nil {
		help = append(help, "enter/T read", "n/N jump")
	}
	if m.actions.postComment != nil {
		help = append(help, "c/C comment")
	}
	if m.actions.approve != nil {
		help = append(help, "a approve")
	}
	return strings.Join(append(help, "w web"), " • ")
}

// prApproveMsg delivers the result of approving a pull request.
type prApproveMsg struct {
	id  int
	err error
}

// approve returns a command that approves pr in the background.
func (m prSelector) approve(pr PullRequest) tea.Cmd {
	approve := m.actions.approve
	return func() tea.Msg { return prApproveMsg{id: pr.ID, err: approve(pr)} }
}

// approved handles the result of approving, returning to the preview either way.
func (m prSelector) approved(msg prApproveMsg) prSelector {
	if m.mode != modeApproving || msg.id != m.pr.ID {
		return m
	}
	m.mode = modeDiff
	if msg.err != nil {
		m.notice, m.noticeOK = fmt.Sprintf("Couldn't approve #%d: %v", msg.id, msg.err), false
		return m
	}
	m.notice, m.noticeOK = fmt.Sprintf("✓ Approved #%d", msg.id), true
	return m
}
