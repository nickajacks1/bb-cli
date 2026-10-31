package bb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"

	"charm.land/huh/v2/spinner"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/urfave/cli/v3"
)

// Pull request states Bitbucket knows, as accepted by --state.
var prStates = []string{"open", "merged", "declined", "superseded"}

func prCommand() *cli.Command {
	return &cli.Command{
		Name:  "pr",
		Usage: "Work with pull requests",
		Commands: []*cli.Command{
			prListCommand(),
			prCreateCommand(),
		},
	}
}

func prListCommand() *cli.Command {
	return &cli.Command{
		Name:      "list",
		Aliases:   []string{"ls"},
		Usage:     "Browse a repository's pull requests",
		ArgsUsage: "[REPO]",
		Description: "REPO defaults to the repository the current git checkout was cloned from.\n\n" +
			"On a terminal this opens an interactive table: move with the arrow keys, press enter to\n" +
			"view the selected pull request's diff, w to open it in your browser (its URL is also\n" +
			"printed), t to read its comments, c to comment on it, q to quit.\n\n" +
			"In the diff preview, s switches between unified and side-by-side layouts (side by side\n" +
			"needs a wide terminal), j/k move a line cursor, c comments on the cursor's line, and\n" +
			"C comments on the pull request as a whole. In the side-by-side layout, h/l choose\n" +
			"whether the old or the new side is commented on. ctrl+s posts a comment.\n\n" +
			"Lines with comments are marked with a dot: n/N jump between them, enter reads the\n" +
			"comments on the cursor's line, and T reads all of them.\n\n" +
			"a approves the pull request (after asking you to confirm), and w opens it in the browser.\n" +
			"The preview's title bar ends with the pull request's build counts, e.g. \"Builds: 3 success,\n" +
			"1 failed, 2 in progress\" (green, red and yellow). States with no builds are left out.\n" +
			"When output is piped, the table is printed instead.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "state",
				Value: "open",
				Usage: "pull request state(s), comma-separated: " + strings.Join(prStates, ", ") + ", or all",
			},
		},
		ShellComplete: completeRepoNames(1),
		Action:        runPRList,
	}
}

func runPRList(ctx context.Context, cmd *cli.Command) error {
	if cmd.NArg() > 1 {
		return errors.New("usage: bb pr list [repo]")
	}
	states, err := parsePRStates(cmd.String("state"))
	if err != nil {
		return err
	}

	cfg, err := requireConfig()
	if err != nil {
		return err
	}

	slug := cmd.Args().First()
	if slug == "" {
		if slug, err = currentRepoSlug(ctx, cfg); err != nil {
			return err
		}
	}

	prs, err := fetchPullRequests(ctx, cfg, slug, states)
	if err != nil {
		return err
	}

	out := cmd.Root().Writer
	if len(prs) == 0 {
		fmt.Fprintf(out, "No %s pull requests in %s/%s\n", strings.ToLower(cmd.String("state")), cfg.Workspace, slug)
		return nil
	}

	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		printPRTable(out, prs)
		return nil
	}

	pr, err := selectPullRequest(prs, prActions{
		loadDiff: func(pr PullRequest) (string, bool, error) {
			return getPullRequestDiff(ctx, cfg, slug, pr.ID)
		},
		loadBuilds: func(pr PullRequest) ([]BuildStatus, error) {
			return listBuildStatuses(ctx, cfg, slug, pr.ID)
		},
		approve: func(pr PullRequest) error {
			return approvePullRequest(ctx, cfg, slug, pr.ID)
		},
		loadComments: func(pr PullRequest) ([]Comment, error) {
			return listComments(ctx, cfg, slug, pr.ID)
		},
		postComment: func(pr PullRequest, c newComment) error {
			return createComment(ctx, cfg, slug, pr.ID, c)
		},
	})
	if err != nil || pr == nil {
		return err
	}
	return openPullRequest(out, os.Stderr, pr, openBrowser)
}

// printPRTable writes the static table, through a pager when it goes to the real stdout.
func printPRTable(out io.Writer, prs []PullRequest) {
	if out == os.Stdout {
		printPaged(renderPRTable(prs), len(prs)+1)
		return
	}
	lipgloss.Fprintln(out, renderPRTable(prs))
}

// openPullRequest prints the pull request's URL to out and tries to open it with open. Not being
// able to open a browser (e.g. over SSH) is only a note on errOut, since the URL was printed.
func openPullRequest(out, errOut io.Writer, pr *PullRequest, open func(string) error) error {
	url := pr.URL()
	if url == "" {
		return fmt.Errorf("pull request #%d has no web link", pr.ID)
	}
	fmt.Fprintln(out, url)
	if err := open(url); err != nil {
		lipgloss.Fprintln(errOut, dimStyle.Render("Couldn't open a browser: "+err.Error()))
	}
	return nil
}

// parsePRStates turns the --state value into Bitbucket's upper-case state names.
func parsePRStates(value string) ([]string, error) {
	var states []string
	for s := range strings.SplitSeq(value, ",") {
		s = strings.ToLower(strings.TrimSpace(s))
		switch {
		case s == "all":
			return upperAll(prStates), nil
		case s == "":
		case slices.Contains(prStates, s):
			states = append(states, strings.ToUpper(s))
		default:
			return nil, fmt.Errorf("invalid --state %q: must be one of %s, or all", s, strings.Join(prStates, ", "))
		}
	}
	if len(states) == 0 {
		return nil, errors.New("--state needs at least one value")
	}
	return states, nil
}

func upperAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToUpper(s)
	}
	return out
}

// currentRepoSlug identifies the repository of the git checkout in the working directory.
func currentRepoSlug(ctx context.Context, cfg *Config) (string, error) {
	hint := fmt.Errorf("can't tell which repository to use: run this inside a checkout of a repository "+
		"in workspace %q, or name one, e.g. `bb pr list <repo>`", cfg.Workspace)

	git, err := exec.LookPath("git")
	if err != nil {
		return "", hint
	}
	slug, ok := checkoutRepoSlug(ctx, git, ".", cfg.Workspace)
	if !ok {
		return "", hint
	}
	return slug, nil
}

// fetchPullRequests loads the pull requests, with a spinner on stderr when it is a terminal.
func fetchPullRequests(ctx context.Context, cfg *Config, slug string, states []string) ([]PullRequest, error) {
	if !term.IsTerminal(os.Stderr.Fd()) {
		return listPullRequests(ctx, cfg, slug, states)
	}

	var (
		prs      []PullRequest
		fetchErr error
	)
	runErr := spinner.New().
		Title("Fetching pull requests...").
		WithOutput(os.Stderr).
		Context(ctx).
		Action(func() { prs, fetchErr = listPullRequests(ctx, cfg, slug, states) }).
		Run()
	if fetchErr != nil {
		return nil, fetchErr
	}
	return prs, runErr
}
