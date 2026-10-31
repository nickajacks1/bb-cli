package bb

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// prCommentsMsg delivers the result of fetching a pull request's comments.
type prCommentsMsg struct {
	id       int
	comments []Comment
	err      error
}

// fetchComments returns a command that loads pr's comments in the background, or nil when
// comments can't be loaded.
func (m prSelector) fetchComments(pr PullRequest) tea.Cmd {
	load := m.actions.loadComments
	if load == nil {
		return nil
	}
	return func() tea.Msg {
		comments, err := load(pr)
		return prCommentsMsg{id: pr.ID, comments: comments, err: err}
	}
}

// inDiff reports whether the diff preview (or something opened from it) is the current screen.
func (m prSelector) inDiff() bool {
	switch m.mode {
	case modeDiff, modeComment, modeSending:
		return true
	case modeThreads:
		return m.threadsBack == modeDiff
	}
	return false
}

// commentsLoaded handles fetched comments: it marks them in the diff, or opens them for
// reading when they were asked for from the list. Results for a screen the user has left are
// ignored.
func (m prSelector) commentsLoaded(msg prCommentsMsg) prSelector {
	if msg.id != m.pr.ID || m.mode == modeList {
		return m
	}
	if msg.err != nil {
		if m.mode == modeLoadingComments {
			m.mode = modeList
		}
		m.notice, m.noticeOK = fmt.Sprintf("Couldn't load the comments of #%d: %v", msg.id, msg.err), false
		return m
	}

	m.threads, m.commentsReady = buildThreads(msg.comments), true
	if m.inDiff() {
		m.viewer.setThreads(m.threads)
	}
	if m.mode == modeLoadingComments {
		m, _ = m.readAllComments(modeList)
	}
	return m
}

// commentsSummary is the diff title's note about the pull request's comments.
func (m prSelector) commentsSummary() string {
	switch {
	case m.actions.loadComments == nil:
		return ""
	case !m.commentsReady:
		return "  loading comments..."
	}
	n := countComments(m.threads)
	return fmt.Sprintf("  %d comment%s", n, map[bool]string{true: "", false: "s"}[n == 1])
}

// readLineComments opens the comments on the diff line under the cursor.
func (m prSelector) readLineComments() (prSelector, tea.Cmd) {
	if m.actions.loadComments == nil {
		return m, nil
	}
	if !m.commentsReady {
		m.notice, m.noticeOK = "Comments are still loading", false
		return m, nil
	}

	var picked []thread
	for _, i := range m.viewer.cursorThreads() {
		picked = append(picked, m.threads[i])
	}
	if len(picked) == 0 {
		m.notice, m.noticeOK = "No comments on this line (n/N jump between commented lines, T lists them all)", false
		return m, nil
	}

	title := "Comments"
	if _, ref, ok := m.viewer.commentLine(); ok {
		title += " on " + inlineDescription(ref)
	}
	return m.openThreads(modeDiff, title, picked), nil
}

// readAllComments opens every comment on the pull request, returning to back afterwards.
func (m prSelector) readAllComments(back prMode) (prSelector, tea.Cmd) {
	if m.actions.loadComments == nil {
		return m, nil
	}
	if !m.commentsReady {
		m.notice, m.noticeOK = "Comments are still loading", false
		return m, nil
	}
	if len(m.threads) == 0 {
		m.mode = back
		m.notice, m.noticeOK = fmt.Sprintf("No comments on #%d yet", m.pr.ID), false
		return m, nil
	}
	return m.openThreads(back, fmt.Sprintf("Comments on #%d %s", m.pr.ID, truncate(m.pr.Title, max(10, m.width-40))), m.threads), nil
}

func (m prSelector) openThreads(back prMode, title string, threads []thread) prSelector {
	m.threadView = newThreadView(title, threads, m.width, m.height-diffChrome)
	m.threadsBack, m.mode = back, modeThreads
	return m
}

func (m prSelector) updateThreads(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key := msg.String(); key {
	case "ctrl+c":
		return m, tea.Quit
	case "q", "esc", "backspace":
		m.mode = m.threadsBack
	default:
		m.threadView.handleKey(key)
	}
	return m, nil
}

func (m prSelector) threadsView() string {
	title := titleStyle.Render(m.threadView.title) +
		dimStyle.Render(fmt.Sprintf("  %s  %d%%", m.threadView.summary(), m.threadView.percent()))
	help := dimStyle.Render(strings.Join([]string{"j/k scroll", "space/b page", "g/G top/bottom", "q back"}, " • "))
	return m.fitLine(title) + "\n" + m.threadView.view() + "\n" + m.fitLine(help)
}
