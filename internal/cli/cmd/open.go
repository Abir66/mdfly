package cmd

import (
	"fmt"
	"io"
	"os/exec"
	"runtime"
)

// openURL launches the system browser for url, reporting a warning to w on
// failure. A browser that fails to open is never fatal — the URL is still on
// stdout for the user to copy.
func openURL(w io.Writer, url string) {
	name, args := openCommand(url)
	if err := exec.Command(name, args...).Start(); err != nil {
		fmt.Fprintf(w, "could not open browser: %v\n", err)
	}
}

// openCommand returns the platform's URL-opening command.
func openCommand(url string) (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		return "xdg-open", []string{url}
	}
}
