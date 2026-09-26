package ui

import (
	"os/exec"
	"runtime"
	"strings"
)

// copyToClipboard writes text to the system clipboard (pbcopy on macOS,
// wl-copy/xclip on Linux). The popup runs locally, so this reaches the OS
// pasteboard directly — independent of tmux.
func copyToClipboard(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	default:
		if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command("wl-copy")
		} else {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		}
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
