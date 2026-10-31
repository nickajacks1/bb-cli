package bb

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

// commentForm is the text box shown when writing a comment.
type commentForm struct {
	open  bool
	input textarea.Model
	ref   *inlineRef // where an inline comment attaches; nil for a comment on the whole pull request
	desc  string     // what the comment is on, for inline comments
	code  string     // the line of code commented on, for inline comments
	back  prMode     // the screen to return to
	err   string     // why the last attempt to post failed
}

func (f *commentForm) resize(width, height int) {
	f.input.SetWidth(max(20, min(width-2, 100)))
	f.input.SetHeight(max(3, min(8, height-8)))
}

// prCommentMsg delivers the result of posting a comment.
type prCommentMsg struct {
	id  int
	err error
}

// openComment switches to the comment box. ref and desc are set for an inline comment.
func (m prSelector) openComment(back prMode, pr PullRequest, ref *inlineRef, desc string) (tea.Model, tea.Cmd) {
	in := textarea.New()
	in.ShowLineNumbers = false
	in.Placeholder = "Write a comment..."

	m.comment = commentForm{open: true, input: in, ref: ref, desc: desc, back: back}
	if ref != nil {
		line, _, _ := m.viewer.commentLine()
		m.comment.code = strings.TrimSpace(line.text)
	}
	m.comment.resize(m.width, m.height)
	m.pr, m.mode = pr, modeComment
	focus := m.comment.input.Focus() // not inline in the return: it must run before m is copied
	return m, focus
}

func (m prSelector) updateComment(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c": // discard; ctrl+c shouldn't throw the whole session away
		m.mode, m.comment.open = m.comment.back, false
		return m, nil
	case "ctrl+s":
		body := strings.TrimSpace(m.comment.input.Value())
		if body == "" {
			m.comment.err = "Write a comment first."
			return m, nil
		}
		m.comment.err = ""
		m.mode = modeSending
		return m, m.postComment(body)
	}

	m.comment.err = ""
	var cmd tea.Cmd
	m.comment.input, cmd = m.comment.input.Update(msg)
	return m, cmd
}

// postComment returns a command that posts body in the background.
func (m prSelector) postComment(body string) tea.Cmd {
	post, pr := m.actions.postComment, m.pr
	c := newComment{Body: body}
	if ref := m.comment.ref; ref != nil {
		c.Path, c.From, c.To = ref.path, ref.from, ref.to
	}
	return func() tea.Msg { return prCommentMsg{id: pr.ID, err: post(pr, c)} }
}

// commentPosted handles the result of posting: on success it returns to the screen the comment
// was started from, on failure it keeps the text so nothing typed is lost.
func (m prSelector) commentPosted(msg prCommentMsg) (prSelector, tea.Cmd) {
	if m.mode != modeSending || msg.id != m.pr.ID {
		return m, nil
	}
	if msg.err != nil {
		m.mode = modeComment
		m.comment.err = fmt.Sprintf("Couldn't post the comment: %v", msg.err)
		return m, nil
	}

	where := fmt.Sprintf("#%d", m.pr.ID)
	if m.comment.ref != nil {
		where = m.comment.desc
	}
	m.mode, m.comment.open = m.comment.back, false
	m.notice, m.noticeOK = "✓ Comment posted on "+where, true
	return m, m.fetchComments(m.pr) // so the new comment shows up
}

func (m prSelector) commentView() string {
	target := fmt.Sprintf("#%d %s", m.pr.ID, truncate(m.pr.Title, max(10, m.width-30)))
	title := "Comment on " + target
	if m.comment.ref != nil {
		title = "Comment on " + m.comment.desc
	}

	var b strings.Builder
	b.WriteString(m.fitLine(titleStyle.Render(title)) + "\n")
	if m.comment.ref != nil {
		b.WriteString(m.fitLine(dimStyle.Render("  "+m.comment.code)) + "\n")
	}
	b.WriteString("\n" + m.comment.input.View() + "\n")

	switch {
	case m.mode == modeSending:
		b.WriteString(dimStyle.Render("Posting comment..."))
	case m.comment.err != "":
		b.WriteString(m.fitLine(errorStyle.Render(m.comment.err)) + "\n")
		fallthrough
	default:
		b.WriteString(dimStyle.Render("ctrl+s post • esc discard"))
	}
	return b.String()
}
