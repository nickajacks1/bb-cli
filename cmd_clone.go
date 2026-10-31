package bb

import (
	"context"

	"github.com/urfave/cli/v3"
)

func cloneCommand() *cli.Command {
	return &cli.Command{
		Name:          "clone",
		Usage:         "Clone a repository",
		ArgsUsage:     "<repo>",
		ShellComplete: completeRepoNames(1),
		Action:        runClone,
	}
}

func runClone(ctx context.Context, cmd *cli.Command) error {
	// TODO: implement
	return nil
}
