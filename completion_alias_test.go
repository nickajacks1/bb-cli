package bb

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func completionScript(t *testing.T, shell string) string {
	t.Helper()
	var buf strings.Builder
	app := NewApp()
	app.Writer = &buf
	if err := app.Run(t.Context(), []string{"bb", "completion", shell}); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestCompletionScriptsAlsoCompleteBBCD(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "pwsh"} {
		script := completionScript(t, shell)

		snippet := bbcdCompletion(shell, "bb")
		if !strings.Contains(strings.TrimSuffix(script, snippet), "--generate-shell-completion") {
			t.Errorf("%s: urfave's script for bb itself should still come first", shell)
		}
		if snippet == "" || !strings.HasSuffix(script, snippet) {
			t.Errorf("%s: the script should end with the %s completion", shell, bbcdAlias)
		}
		if strings.Contains(snippet, "@") {
			t.Errorf("%s: unreplaced placeholder in %q", shell, snippet)
		}
		if !strings.Contains(snippet, "bbcd") || !strings.Contains(snippet, "bb clone --generate-shell-completion") {
			t.Errorf("%s: snippet should register bbcd using bb's repo name completion:\n%s", shell, snippet)
		}
	}
}

func TestBBCDCompletionUsesTheProgramName(t *testing.T) {
	if got := bbcdCompletion("bash", "mybb"); !strings.Contains(got, "mybb clone --generate-shell-completion") || strings.Contains(got, "bb clone --generate-shell-completion\n") {
		t.Errorf("snippet = %q", got)
	}
	if bbcdCompletion("tcsh", "bb") != "" {
		t.Error("an unknown shell has no snippet")
	}
}

// The snippets get repository names by running `bb clone --generate-shell-completion`. Pin that
// this really is the cache-backed repository name completion.
func TestBBCDCompletionRelyOnCloneRepoNames(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if _, err := saveConfig(&Config{Workspace: "ws", Email: "e", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := saveRepoCache("ws", []Repository{{Slug: "alpha"}, {Slug: "bravo"}}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(complete(t, "clone", "--generate-shell-completion"), ","); got != "alpha,bravo" {
		t.Errorf("clone's completion = %q; the bbcd snippets depend on it listing repository names", got)
	}
}

func TestCompletionCommandIsDocumentedAndVisible(t *testing.T) {
	var out strings.Builder
	app := NewApp()
	app.Writer = &out
	if err := app.Run(t.Context(), []string{"bb", "--help"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "completion") {
		t.Errorf("completion should be listed in help:\n%s", out.String())
	}

	out.Reset()
	if err := app.Run(t.Context(), []string{"bb", "completion", "--help"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "bbcd") {
		t.Errorf("completion help should explain bbcd:\n%s", out.String())
	}
}

// fakeBB puts a `bb` on PATH that answers the repository completion request the way bb does:
// one name per line, some with a description.
func fakeBB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1 $2\" = \"clone --generate-shell-completion\" ]; then printf 'alpha\\nalps:a description\\nbeta\\n'; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "bb"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

func runShell(t *testing.T, shell, path string, args ...string) string {
	t.Helper()
	cmd := exec.Command(shell, args...)
	cmd.Env = append(os.Environ(), "PATH="+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", shell, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestBBCDCompletionInBash(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	path := fakeBB(t)

	reply := func(words ...string) string {
		quoted := make([]string, len(words))
		for i, w := range words {
			quoted[i] = "'" + w + "'"
		}
		script := bbcdCompletion("bash", "bb") + "\nCOMP_WORDS=(" + strings.Join(quoted, " ") + ")\n" +
			"COMP_CWORD=$((${#COMP_WORDS[@]}-1))\n__bbcd_autocomplete\necho \"[${COMPREPLY[*]}]\"\ncomplete -p bbcd"
		return runShell(t, "bash", path, "-c", script)
	}

	tests := []struct {
		name  string
		words []string
		want  string
	}{
		{"nothing typed", []string{"bbcd", ""}, "[alpha alps beta]"}, // descriptions are stripped
		{"a prefix", []string{"bbcd", "al"}, "[alpha alps]"},
		{"no match", []string{"bbcd", "zz"}, "[]"},
		{"only one repository is taken", []string{"bbcd", "alpha", ""}, "[]"},
	}
	for _, tc := range tests {
		got := reply(tc.words...)
		first, registered, _ := strings.Cut(got, "\n")
		if first != tc.want || !strings.Contains(registered, "-F __bbcd_autocomplete bbcd") {
			t.Errorf("%s: got %q, want %q (registered: %q)", tc.name, first, tc.want, registered)
		}
	}
}

func TestBBCDCompletionInFish(t *testing.T) {
	if _, err := exec.LookPath("fish"); err != nil {
		t.Skip("fish not available")
	}
	path := fakeBB(t)

	complete := func(line string) string {
		script := bbcdCompletion("fish", "bb") + "\ncomplete -C " + "'" + line + "'"
		return runShell(t, "fish", path, "-c", script)
	}
	if got := complete("bbcd "); got != "alpha\nalps\nbeta" {
		t.Errorf("nothing typed: %q", got)
	}
	if got := complete("bbcd al"); got != "alpha\nalps" {
		t.Errorf("prefix: %q", got)
	}
	// After the repository there is nothing to offer, not even file names.
	if got := complete("bbcd alpha "); got != "" {
		t.Errorf("second argument: %q", got)
	}
}

func TestBBCDCompletionInZsh(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not available")
	}
	path := fakeBB(t)

	// Stub the completion builtins: compadd prints the elements of the array it is given.
	run := func(current int) string {
		script := "compdef() { :; }\ncompadd() { print -r -- \"${(j: :)${(P)2}}\"; }\n" +
			bbcdCompletion("zsh", "bb") + "\nCURRENT=" + string(rune('0'+current)) + "\n_bbcd\nprint \"rc=$?\""
		return runShell(t, "zsh", path, "-f", "-c", script)
	}
	if got := run(2); got != "alpha alps beta\nrc=0" {
		t.Errorf("first argument: %q", got)
	}
	if got := run(3); got != "rc=1" {
		t.Errorf("second argument: %q", got)
	}
}
