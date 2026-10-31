package bb

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
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
	switch key := msg.String(); key {
	case "ctrl+c":
		return m, tea.Quit
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
		line, ref, ok := m.viewer.commentLine()
		if !ok {
			m.notice, m.noticeOK = "Can't comment on this line: move to an added, removed or unchanged line (C comments on the pull request)", false
			return m, nil
		}
		return m.openComment(modeDiff, m.pr, &ref, inlineDescription(ref, line))
	case "C":
		if m.actions.postComment == nil {
			return m, nil
		}
		return m.openComment(modeDiff, m.pr, nil, "")
	default:
		m.viewer.handleKey(key)
	}
	return m, nil
}

// inlineDescription says where an inline comment will attach, e.g. "f.go:34 (new)".
func inlineDescription(ref inlineRef, line diffLine) string {
	if ref.from > 0 {
		return fmt.Sprintf("%s:%d (old)", ref.path, ref.from)
	}
	return fmt.Sprintf("%s:%d (new)", ref.path, ref.to)
}

func (m prSelector) diffView() string {
	layout := "unified"
	if m.viewer.rowsSplit {
		layout = "split"
	}
	title := titleStyle.Render(fmt.Sprintf("#%d %s", m.pr.ID, truncate(m.pr.Title, max(10, m.width-30))))
	title += dimStyle.Render(fmt.Sprintf("  %d%%  %s", m.viewer.percent(), layout))

	var footer string
	if m.notice != "" {
		footer = m.noticeStyled()
	} else {
		footer = dimStyle.Render(m.diffNote + m.diffHelp())
	}
	return m.fitLine(title) + "\n" + m.viewer.view() + "\n" + m.fitLine(footer)
}

func (m prSelector) diffHelp() string {
	help := []string{"j/k move", "space/b page", "g/G top/bottom"}
	if m.viewer.rowsSplit {
		help = append(help, "←/→ side", "s unified")
	} else {
		help = append(help, "←/→ pan", "s split")
	}
	if m.actions.postComment != nil {
		help = append(help, "c comment line", "C comment PR")
	}
	return strings.Join(append(help, "q back"), " • ")
}
