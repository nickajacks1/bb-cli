package bb

import (
	"context"
	"fmt"
	"strings"

	"github.com/urfave/cli/v3"
)

// bbcdAlias is the name of a shell alias or function users define themselves to cd into a
// repository under bb's home directory. bb can't create it (it has to change the shell's
// directory), but `bb completion` makes it complete repository names.
const bbcdAlias = "bbcd"

// bbcdCompletions holds, per shell, the completion for bbcdAlias appended to the script urfave
// generates. @BB@ is the program name and @ALIAS@ the alias. Names come from `@BB@ clone`'s
// completion, which reads the local cache and never touches the network; the alias takes one
// repository, so nothing is offered after it.
var bbcdCompletions = map[string]string{
	"bash": `
# Complete @ALIAS@, a user-defined alias or function that cds to a repository under bb's home.
__@ALIAS@_autocomplete() {
  COMPREPLY=()
  [[ "${COMP_CWORD}" -eq 1 ]] || return 0
  local repos
  repos="$(@BB@ clone --generate-shell-completion 2>/dev/null | cut -d: -f1)"
  COMPREPLY=( $(compgen -W "${repos}" -- "${COMP_WORDS[COMP_CWORD]}") )
}
complete -F __@ALIAS@_autocomplete @ALIAS@
`,

	"zsh": `
# Complete @ALIAS@, a user-defined alias or function that cds to a repository under bb's home.
_@ALIAS@() {
	(( CURRENT == 2 )) || return 1
	local -a repos
	repos=(${(f)"$(@BB@ clone --generate-shell-completion 2>/dev/null)"})
	repos=(${repos%%:*})
	compadd -a repos
}
compdef _@ALIAS@ @ALIAS@
`,

	"fish": `
# Complete @ALIAS@, a user-defined alias or function that cds to a repository under bb's home.
# fish only loads this once it has loaded the completions for @BB@ itself.
function __@ALIAS@_repos
    @BB@ clone --generate-shell-completion 2>/dev/null | string replace -r ':.*' '' | string match -r '.+'
end
complete -c @ALIAS@ -f
complete -c @ALIAS@ -n 'test (count (commandline -opc)) -eq 1' -a '(__@ALIAS@_repos)'
`,

	"pwsh": `
# Complete @ALIAS@, a user-defined alias or function that cds to a repository under bb's home.
Register-ArgumentCompleter -Native -CommandName @ALIAS@ -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    $before = $commandAst.CommandElements.Count
    if ($wordToComplete -ne '') { $before-- }
    if ($before -ne 1) { return }
    & @BB@ clone --generate-shell-completion 2>$null | ForEach-Object {
        $name = $_.Split(':', 2)[0].Trim()
        if ($name -and $name -like "$wordToComplete*") {
            [System.Management.Automation.CompletionResult]::new($name, $name, 'ParameterValue', $name)
        }
    }
}
`,
}

// bbcdCompletion returns the bbcd completion for a shell, or "" for a shell without one.
func bbcdCompletion(shell, program string) string {
	return strings.NewReplacer("@BB@", program, "@ALIAS@", bbcdAlias).Replace(bbcdCompletions[shell])
}

// configureCompletionCommand makes `completion` visible in help (urfave hides it) and has each
// shell's script also complete bbcdAlias.
func configureCompletionCommand(c *cli.Command) {
	c.Hidden = false
	c.Description += fmt.Sprintf("\nThe script also completes %[1]s, a shell alias or function you can define to\n"+
		"cd to a repository under bb's home directory, e.g. in bash:\n\n"+
		"   %[1]s() {\n"+
		"       local BB_HOME\n"+
		"       BB_HOME=$(bb config get home) || return\n"+
		"       [ -d \"$BB_HOME/$1\" ] || bb clone \"$1\"\n"+
		"       cd \"$BB_HOME/$1\"\n"+
		"   }\n", bbcdAlias)

	for _, shell := range c.Commands {
		generate, name := shell.Action, shell.Name
		shell.Action = func(ctx context.Context, cmd *cli.Command) error {
			if err := generate(ctx, cmd); err != nil {
				return err
			}
			_, err := fmt.Fprint(cmd.Root().Writer, bbcdCompletion(name, cmd.Root().Name))
			return err
		}
	}
}
