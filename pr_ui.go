package bb

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	maxPRTitleLen  = 60
	maxPRBranchLen = 30
)

var prHeaders = []string{"#", "TITLE", "AUTHOR", "BRANCH", "UPDATED"}

func prRows(prs []PullRequest) [][]string {
	rows := make([][]string, len(prs))
	for i, p := range prs {
		rows[i] = []string{
			fmt.Sprintf("#%d", p.ID),
			truncate(p.Title, maxPRTitleLen),
			truncate(p.Author.DisplayName, maxPRBranchLen),
			truncate(p.Source.Branch.Name, maxPRBranchLen),
			p.UpdatedOn.Local().Format(time.DateOnly),
		}
	}
	return rows
}

// renderPRTable renders a static table, used when output isn't an interactive terminal.
func renderPRTable(prs []PullRequest) string {
	return renderTable(prHeaders, prRows(prs), 0)
}

// prMode says which screen the pull request selector is showing.
type prMode int

const (
	modeList prMode = iota
	modeLoadingDiff
	modeDiff
	modeComment
	modeSending
)

// prActions are the things the selector can do beyond choosing a pull request. A nil action
// disables its key.
type prActions struct {
	// loadDiff fetches a pull request's unified diff (truncated reports it was cut short).
	loadDiff func(PullRequest) (diff string, truncated bool, err error)
	// postComment posts a comment on a pull request.
	postComment func(PullRequest, newComment) error
}

// prSelector is an interactive table of pull requests. Enter picks one; v previews its diff
// (where c and C comment); c comments on the highlighted pull request; q/esc/ctrl+c cancels.
type prSelector struct {
	table    table.Model
	prs      []PullRequest
	selected *PullRequest
	actions  prActions

	width, height int
	mode          prMode
	pr            PullRequest // the pull request being previewed or commented on

	split    bool       // preferred diff layout; sticks across previews
	viewer   diffViewer // the diff preview
	diffNote string     // shown in the preview's help line, e.g. that the diff was cut short

	comment commentForm

	notice   string // one-off message, cleared by the next key press
	noticeOK bool   // whether the notice reports success rather than a problem
}

func newPRSelector(prs []PullRequest, actions prActions) prSelector {
	rows := prRows(prs)

	// Size each column to its widest cell so nothing is cut off.
	cols := make([]table.Column, len(prHeaders))
	for i, h := range prHeaders {
		cols[i] = table.Column{Title: h, Width: lipgloss.Width(h)}
	}
	tableRows := make([]table.Row, len(rows))
	for r, row := range rows {
		tableRows[r] = row
		for c, cell := range row {
			cols[c].Width = max(cols[c].Width, lipgloss.Width(cell))
		}
	}
	width := 0 // the viewport clips to its width, so it must cover every padded column
	for _, c := range cols {
		width += c.Width + 2
	}

	styles := table.DefaultStyles()
	styles.Header = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")).Padding(0, 1)
	styles.Selected = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("111")).Reverse(true)

	t := table.New(
		table.WithColumns(cols),
		table.WithRows(tableRows),
		table.WithStyles(styles),
		table.WithFocused(true),
		table.WithWidth(width),
		table.WithHeight(min(len(rows), 15)+1),
	)
	return prSelector{table: t, prs: prs, actions: actions, width: 80, height: 24}
}

func (m prSelector) Init() tea.Cmd { return nil }

func (m prSelector) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case prDiffMsg:
		return m.diffLoaded(msg), nil
	case prCommentMsg:
		return m.commentPosted(msg), nil
	case tea.KeyPressMsg:
		switch m.mode {
		case modeLoadingDiff:
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "q", "esc":
				m.mode = modeList
			}
			return m, nil
		case modeDiff:
			return m.updateDiff(msg)
		case modeComment:
			return m.updateComment(msg)
		case modeSending:
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			return m, nil
		}
		return m.updateList(msg)
	}

	if m.mode == modeComment { // cursor blinking and the like
		var cmd tea.Cmd
		m.comment.input, cmd = m.comment.input.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m prSelector) updateList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.notice = ""
	switch msg.String() {
	case "enter":
		m.selected = &m.prs[m.table.Cursor()]
		return m, tea.Quit
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "v":
		if m.actions.loadDiff == nil {
			return m, nil
		}
		m.mode, m.pr = modeLoadingDiff, m.prs[m.table.Cursor()]
		return m, m.fetchDiff(m.pr)
	case "c":
		if m.actions.postComment == nil {
			return m, nil
		}
		return m.openComment(modeList, m.prs[m.table.Cursor()], nil, "")
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m *prSelector) resize(width, height int) {
	m.width, m.height = width, height
	// Leave room for the help line and a blank line; the header row counts toward the height.
	m.table.SetHeight(max(3, min(len(m.prs)+1, height-3)))
	m.viewer.resize(width, height-diffChrome)
	if m.comment.open {
		m.comment.resize(width, height)
	}
}

func (m prSelector) View() tea.View {
	var content string
	switch m.mode {
	case modeLoadingDiff:
		content = dimStyle.Render(fmt.Sprintf("Loading the diff of #%d...  (q to cancel)", m.pr.ID))
	case modeDiff:
		content = m.diffView()
	case modeComment, modeSending:
		content = m.commentView()
	default:
		content = m.listView()
	}

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

func (m prSelector) listView() string {
	help := "↑/↓ navigate • enter open in browser"
	if m.actions.loadDiff != nil {
		help += " • v view diff"
	}
	if m.actions.postComment != nil {
		help += " • c comment"
	}
	help += " • q quit"

	out := m.table.View() + "\n\n"
	if m.notice != "" {
		out += m.noticeStyled() + "\n"
	}
	return out + dimStyle.Render(help)
}

func (m prSelector) noticeStyled() string {
	if m.noticeOK {
		return successStyle.Render(m.notice)
	}
	return errorStyle.Render(m.notice)
}

// fitLine cuts a styled-or-plain line to the screen width so a long one can't wrap and push
// the rest of the layout down.
func (m prSelector) fitLine(s string) string { return ansi.Truncate(s, max(1, m.width), "…") }

// selectPullRequest shows the interactive table and returns the chosen pull request, or nil if
// the user cancelled.
func selectPullRequest(prs []PullRequest, actions prActions) (*PullRequest, error) {
	final, err := tea.NewProgram(newPRSelector(prs, actions)).Run()
	if err != nil {
		return nil, err
	}
	return final.(prSelector).selected, nil
}
