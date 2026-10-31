package bb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"charm.land/lipgloss/v2"
	"github.com/urfave/cli/v3"
)

func cloneCommand() *cli.Command {
	return &cli.Command{
		Name:      "clone",
		Usage:     "Clone a repository",
		ArgsUsage: "<repo> [destination]",
		Description: "Without a destination the repository is cloned into <home directory>/<repo>.\n" +
			"With one it is cloned there instead, like git clone:\n" +
			"   bb clone foo        clone into the home directory\n" +
			"   bb clone foo .      clone into the current directory (must be empty)\n" +
			"   bb clone foo bar    clone into ./bar\n\n" +
			"If the destination is already a checkout of the repository (over SSH or HTTPS), nothing is\n" +
			"cloned and its location is reported instead.\n\n" +
			"The home directory is set with `bb configure`. Cloning uses SSH unless --https is given.",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "https",
				Usage: "clone over HTTPS instead of SSH",
			},
		},
		ShellComplete: completeRepoNames(1),
		Action:        runClone,
	}
}

func runClone(ctx context.Context, cmd *cli.Command) error {
	if n := cmd.Args().Len(); n < 1 || n > 2 {
		return errors.New("usage: bb clone <repo> [destination]")
	}
	slug, dest := cmd.Args().Get(0), cmd.Args().Get(1)

	cfg, err := requireConfig()
	if err != nil {
		return err
	}

	// Check the home directory before any network call so a missing config fails fast.
	if dest == "" {
		dir, err := cfg.requireHome(fmt.Sprintf(
			"or give a destination: `bb clone %[1]s .` (current directory) or `bb clone %[1]s <path>`", slug))
		if err != nil {
			return err
		}
		dest = filepath.Join(dir, slug)
	}

	git, err := exec.LookPath("git")
	if err != nil {
		return errors.New("git is required to clone but wasn't found on your PATH")
	}

	repo, err := getRepository(ctx, cfg, slug)
	if err != nil {
		return err
	}

	shown := dest
	if abs, err := filepath.Abs(dest); err == nil {
		shown = abs
	}
	out := cmd.Root().Writer

	// Already cloned (over either protocol)? Then there's nothing to do.
	if existingCheckout(ctx, git, dest, repo.cloneURLs()) {
		lipgloss.Fprintln(out, successStyle.Render(fmt.Sprintf("✓ %s is already checked out at %s", repo.Slug, shown)))
		return nil
	}

	protocol := "ssh"
	if cmd.Bool("https") {
		protocol = "https"
	}
	url, err := repo.cloneURL(protocol)
	if err != nil {
		return err
	}

	// Let git talk to the terminal directly so its progress output and any credential prompts work.
	clone := exec.CommandContext(ctx, git, "clone", "--", url, dest)
	clone.Stdin, clone.Stdout, clone.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := clone.Run(); err != nil {
		return fmt.Errorf("git clone failed: %w", err)
	}

	lipgloss.Fprintln(out, successStyle.Render(fmt.Sprintf("✓ Cloned %s to %s", repo.Slug, shown)))
	return nil
}
