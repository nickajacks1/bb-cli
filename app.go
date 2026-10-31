package bb

import "github.com/urfave/cli/v3"

// NewApp builds the root `bb` command.
func NewApp() *cli.Command {
	return &cli.Command{
		Name:                  "bb",
		Usage:                 "A command line interface for Bitbucket",
		EnableShellCompletion: true,
		ConfigureShellCompletionCommand: func(c *cli.Command) {
			c.Hidden = false // urfave hides it by default
		},
		Commands: []*cli.Command{
			listCommand(),
			configureCommand(),
			cloneCommand(),
		},
	}
}
