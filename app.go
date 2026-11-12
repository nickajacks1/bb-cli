package bb

import (
	"context"

	"github.com/urfave/cli/v3"
)

// NewApp builds the root `bb` command.
func NewApp() *cli.Command {
	return &cli.Command{
		Name:                  "bb",
		Usage:                 "A command line interface for Bitbucket",
		EnableShellCompletion: true,
		ConfigureShellCompletionCommand: func(c *cli.Command) {
			c.Hidden = false // urfave hides it by default
		},
		// Keep Run's errors available to the caller. The executable reports them in main,
		// while tests and library users can inspect them without the CLI exiting the process.
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Commands: []*cli.Command{
			listCommand(),
			configureCommand(),
			cloneCommand(),
			openCommand(),
			prCommand(),
		},
	}
}
