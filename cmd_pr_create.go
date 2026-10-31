package bb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/urfave/cli/v3"
)

func prCreateCommand() *cli.Command {
	return &cli.Command{
		Name:      "create",
		Usage:     "Open a pull request",
		ArgsUsage: "[REPO]",
		Description: "REPO and the source branch default to those of the current git checkout; push the\n" +
			"branch first. The destination defaults to the repository's main branch.\n\n" +
			"On a terminal, a missing title (and the description) is asked for interactively. Otherwise\n" +
			"--title is required. The new pull request's URL is printed to stdout.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "title", Aliases: []string{"t"}, Usage: "pull request title"},
			&cli.StringFlag{Name: "body", Aliases: []string{"b"}, Usage: "pull request description"},
			&cli.StringFlag{Name: "source", Aliases: []string{"s"}, Usage: "source branch (default: the checked-out branch)"},
			&cli.StringFlag{Name: "destination", Aliases: []string{"d"}, Usage: "destination branch (default: the repository's main branch)"},
			&cli.BoolFlag{Name: "draft", Usage: "open as a draft"},
		},
		ShellComplete: completeRepoNames(1),
		Action:        runPRCreate,
	}
}

func runPRCreate(ctx context.Context, cmd *cli.Command) error {
	if cmd.NArg() > 1 {
		return errors.New("usage: bb pr create [repo]")
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

	source := strings.TrimSpace(cmd.String("source"))
	subject := ""
	if source == "" {
		if source, subject, err = checkoutBranch(ctx, cfg, slug); err != nil {
			return err
		}
	}

	destination := strings.TrimSpace(cmd.String("destination"))
	if destination == "" {
		repo, err := getRepository(ctx, cfg, slug)
		if err != nil {
			return err
		}
		if repo.MainBranch == nil {
			return fmt.Errorf("repository %q has no main branch: pass --destination", slug)
		}
		destination = repo.MainBranch.Name
	}
	if source == destination {
		return fmt.Errorf("the source and destination are both %q: pass --source or --destination to pick different branches", source)
	}

	title, body := strings.TrimSpace(cmd.String("title")), cmd.String("body")
	if title == "" {
		if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
			return errors.New("no title: pass --title")
		}
		if title, body, err = promptPRDetails(subject, body); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				lipgloss.Fprintln(os.Stderr, dimStyle.Render("Cancelled, no pull request created."))
				return nil
			}
			return err
		}
	}

	pr, err := createPullRequest(ctx, cfg, slug, newPullRequest{
		Title:       title,
		Description: body,
		Source:      source,
		Destination: destination,
		Draft:       cmd.Bool("draft"),
	})
	if err != nil {
		return err
	}

	lipgloss.Fprintln(os.Stderr, successStyle.Render(fmt.Sprintf("✓ Created pull request #%d: %s (%s → %s)", pr.ID, pr.Title, source, destination)))
	fmt.Fprintln(cmd.Root().Writer, pr.URL())
	return nil
}

// checkoutBranch returns the branch checked out in the working directory and its latest commit
// subject, provided the checkout is of repository slug. Otherwise the branch can't be assumed.
func checkoutBranch(ctx context.Context, cfg *Config, slug string) (branch, subject string, err error) {
	hint := fmt.Errorf("can't tell which branch to open the pull request from: run this on a branch inside "+
		"a checkout of %q, or pass --source <branch>", slug)

	git, lookErr := exec.LookPath("git")
	if lookErr != nil {
		return "", "", hint
	}
	if checkedOut, ok := checkoutRepoSlug(ctx, git, ".", cfg.Workspace); !ok || !strings.EqualFold(checkedOut, slug) {
		return "", "", hint
	}
	if branch, err = currentBranch(ctx, git, "."); err != nil {
		return "", "", hint
	}
	return branch, lastCommitSubject(ctx, git, "."), nil
}

// promptPRDetails asks for the title (prefilled with title) and description (prefilled with body).
func promptPRDetails(title, body string) (string, string, error) {
	err := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Title").
			Value(&title).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("title is required")
				}
				return nil
			}),
		huh.NewText().
			Title("Description (optional)").
			Value(&body),
	)).Run()
	return strings.TrimSpace(title), strings.TrimSpace(body), err
}
