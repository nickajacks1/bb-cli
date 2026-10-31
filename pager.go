package bb

import (
	"os"
	"os/exec"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
)

// printPaged writes s to stdout, sending it through a pager when stdout is a terminal
// and the output (lines long) wouldn't fit on screen. When stdout is redirected or piped
// the output is written directly, with colors stripped.
func printPaged(s string, lines int) {
	fd := os.Stdout.Fd()
	if term.IsTerminal(fd) {
		if _, height, err := term.GetSize(fd); err == nil && lines >= height {
			if runPager(s) {
				return
			}
		}
	}
	lipgloss.Println(s)
}

// runPager pipes s through the user's pager and reports whether it ran.
func runPager(s string) bool {
	args := strings.Fields(os.Getenv("PAGER"))
	if len(args) == 0 {
		args = []string{"less"}
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = strings.NewReader(s + "\n")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Make less show our colors and quit immediately if the output fits, unless the user configured it.
	if _, ok := os.LookupEnv("LESS"); !ok {
		cmd.Env = append(os.Environ(), "LESS=FRX")
	}

	if err := cmd.Start(); err != nil {
		return false // pager not installed; caller prints directly
	}
	_ = cmd.Wait() // a non-zero exit just means the user quit the pager oddly
	return true
}
