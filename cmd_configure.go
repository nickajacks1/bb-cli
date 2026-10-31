package bb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/huh/v2"
	"charm.land/huh/v2/spinner"
	"charm.land/lipgloss/v2"
	"github.com/urfave/cli/v3"
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

func configureCommand() *cli.Command {
	return &cli.Command{
		Name:  "configure",
		Usage: "Interactively set up workspace and credentials",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "reauth",
				Usage: "walk through creating and entering a new API token even if one is already set",
			},
		},
		Action: runConfigure,
	}
}

func runConfigure(ctx context.Context, cmd *cli.Command) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	lipgloss.Println(titleStyle.Render("bb configure"))
	lipgloss.Println(dimStyle.Render("Bitbucket Cloud authenticates with an Atlassian API token."))
	fmt.Println()

	// With a token already stored, skip the "create a token" step unless asked to start over.
	reauth := cmd.Bool("reauth")
	keepingToken := !needsTokenSetup(cfg, reauth)
	if !keepingToken {
		cfg.Token = "" // reauthenticating means entering a new one, not accepting the old
		if err := offerTokenCreation(); err != nil {
			return handleAbort(err)
		}
	}

	for {
		if err := promptCredentials(cfg, keepingToken); err != nil {
			return handleAbort(err)
		}

		checkErr := spinner.New().
			Title("Verifying credentials...").
			Context(ctx).
			ActionWithErr(func(ctx context.Context) error { return checkCredentials(ctx, cfg) }).
			Run()
		if checkErr == nil {
			lipgloss.Println(successStyle.Render("✓ Credentials verified"))
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		lipgloss.Println(errorStyle.Render("✗ " + checkErr.Error()))

		var choice string
		err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("What now?").
				Options(
					huh.NewOption("Edit the details and try again", "retry"),
					huh.NewOption("Save anyway", "save"),
					huh.NewOption("Cancel", "cancel"),
				).
				Value(&choice),
		)).RunWithContext(ctx)
		if err != nil {
			return handleAbort(err)
		}
		if choice == "cancel" {
			return handleAbort(huh.ErrUserAborted)
		}
		if choice == "save" {
			break
		}
	}

	path, err := saveConfig(cfg)
	if err != nil {
		return err
	}
	lipgloss.Println(successStyle.Render("✓ Saved config to " + path))
	if cfg.Home == "" {
		lipgloss.Println(dimStyle.Render("No home directory set: `bb clone <repo>` needs a destination, e.g. `bb clone <repo> .`"))
	}
	return nil
}

// needsTokenSetup reports whether configure should walk the user through creating a token:
// when none is stored yet, or when they asked to start over with --reauth.
func needsTokenSetup(cfg *Config, reauth bool) bool {
	return cfg.Token == "" || reauth
}

// tokenDescription is the help text under the token field. When an existing token is being
// kept, it points at --reauth for replacing it.
func tokenDescription(keepingToken bool) string {
	if keepingToken {
		return "Press enter to keep the current token, or run `bb configure --reauth` to create a new one"
	}
	return "Paste the token you just created"
}

// offerTokenCreation optionally opens the Atlassian token page, listing the scopes to select.
func offerTokenCreation() error {
	open := true // we only ask when a token is needed, so default to helping
	scopes := "Create a token with scopes, choose Bitbucket as the app, and select:\n  • " +
		strings.Join(requiredScopes, "\n  • ")

	err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Open the Atlassian API token page in your browser?").
			Description(scopes).
			Affirmative("Open browser").
			Negative("I already have a token").
			Value(&open),
	)).Run()
	if err != nil || !open {
		return err
	}

	if err := openBrowser(apiTokenURL); err != nil {
		lipgloss.Println(errorStyle.Render("Couldn't launch a browser: " + err.Error()))
	}
	lipgloss.Println(dimStyle.Render("If nothing opened, visit: " + apiTokenURL))
	fmt.Println()
	return nil
}

// promptCredentials asks for the Bitbucket workspace, email, token and local home directory,
// pre-filled from cfg. keepingToken says cfg.Token is an existing token the user can accept as-is.
func promptCredentials(cfg *Config, keepingToken bool) error {
	required := func(name string) func(string) error {
		return func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New(name + " is required")
			}
			return nil
		}
	}

	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Bitbucket workspace").
				Description("The slug in bitbucket.org/<workspace>/<repo>").
				Value(&cfg.Workspace).
				Validate(required("workspace")),
			huh.NewInput().
				Title("Atlassian account email").
				Description("Used as the username for API requests").
				Value(&cfg.Email).
				Validate(required("email")),
			huh.NewInput().
				Title("API token").
				Description(tokenDescription(keepingToken)).
				EchoMode(huh.EchoModePassword).
				Value(&cfg.Token).
				Validate(required("token")),
			huh.NewInput().
				Title("Home directory (optional)").
				Description("bb's home for cloned repositories (not your $HOME). Leave blank to always give a destination.").
				Placeholder("~/code").
				Value(&cfg.Home).
				Validate(func(s string) error {
					_, err := normalizeHome(s)
					return err
				}),
		),
	).Run()

	cfg.Workspace = strings.TrimSpace(cfg.Workspace)
	cfg.Email = strings.TrimSpace(cfg.Email)
	cfg.Token = strings.TrimSpace(cfg.Token)
	if err != nil {
		return err
	}

	cfg.Home, err = normalizeHome(cfg.Home)
	return err
}

// handleAbort turns a user cancellation into a clean exit.
func handleAbort(err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		lipgloss.Println(dimStyle.Render("Cancelled, nothing saved."))
		return nil
	}
	return err
}
