package bb

import (
	"os/exec"
	"runtime"
)

// launchBrowser opens a URL in the user's browser. Commands call it rather than openBrowser,
// so tests can replace it and never launch a real browser.
var launchBrowser = openBrowser

// openBrowser asks the OS to open url in the default browser without waiting for it.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
