package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Environment variables used to hand the parent's resolved paths to a console
// started in a new terminal window.
const (
	EnvConsoleDir  = "NORVET_CONSOLE_DIR"
	EnvConsoleRoot = "NORVET_CONSOLE_ROOT"
)

// The runner must survive Ctrl+C. The terminal is a foreground process group,
// so Ctrl+C reaches this wrapper shell as well as the console. dash (/bin/sh on
// Debian/Ubuntu) dies on SIGINT immediately, and terminal emulators then keep
// the window open ("child exited abnormally"). A trap handler (unlike "ignore")
// is reset to default in the console it starts, so the console still sees
// Ctrl+C, and this shell lives on to close the window with a clean status.
const unixRunner = `trap : INT
cd -- "$1" 2>/dev/null || cd -- "${HOME:-/}" 2>/dev/null || cd /
shift
"$@"
status=$?
# 0 = clean exit; 129/130/143 = hang-up / Ctrl+C / terminate. All of them are
# deliberate ways of closing the console, so the window is disposed at once.
case "$status" in
  0|129|130|143) exit 0 ;;
esac
printf '\n[console] exited with status %s. Press [Enter] to close this window...' "$status"
read -r _
exit "$status"`

const terminalLaunchWait = 900 * time.Millisecond

type terminalCandidate struct {
	name     string
	wrap     func(command []string) []string
	desktops []string
}

func prefix(flags ...string) func([]string) []string {
	return func(command []string) []string {
		return append(append([]string{}, flags...), command...)
	}
}

var terminalCandidates = []terminalCandidate{
	{"gnome-terminal", prefix("--"), []string{"gnome", "unity", "budgie", "pantheon"}},
	{"ptyxis", prefix("--"), []string{"gnome"}},
	{"konsole", prefix("-e"), []string{"kde"}},
	{"xfce4-terminal", prefix("-x"), []string{"xfce"}},
	{"mate-terminal", prefix("-x"), []string{"mate"}},
	{"tilix", prefix("-e"), nil},
	{"terminator", prefix("-x"), nil},
	{"kitty", prefix(), nil},
	{"alacritty", prefix("-e"), nil},
	{"foot", prefix(), nil},
	{"wezterm", prefix("start", "--"), nil},
	{"x-terminal-emulator", prefix("-e"), nil},
	{"xterm", prefix("-e"), nil},
}

func orderedCandidates() []terminalCandidate {
	desktop := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP") + ":" + os.Getenv("DESKTOP_SESSION"))
	var native, rest []terminalCandidate
	for _, candidate := range terminalCandidates {
		matched := false
		for _, fragment := range candidate.desktops {
			if strings.Contains(desktop, fragment) {
				matched = true
				break
			}
		}
		if matched {
			native = append(native, candidate)
		} else {
			rest = append(rest, candidate)
		}
	}
	return append(native, rest...)
}

// ChildEnv returns the environment for a console started in a new terminal.
func ChildEnv(consoleDir, root string) []string {
	return append(os.Environ(), EnvConsoleDir+"="+consoleDir, EnvConsoleRoot+"="+root)
}

// OpenInNewTerminal starts this executable with args inside a new terminal window.
func OpenInNewTerminal(consoleDir, root, executable string, args ...string) error {
	switch runtime.GOOS {
	case "darwin":
		return openMacTerminal(consoleDir, root, executable, args...)
	case "windows":
		return OpenConsoleWindow(consoleDir, ChildEnv(consoleDir, root), executable, args, "Console")
	case "plan9", "js", "wasip1", "ios", "android":
		return fmt.Errorf("opening a new terminal is not supported on %s", runtime.GOOS)
	}
	return openUnixTerminal(consoleDir, root, executable, args...)
}

func openUnixTerminal(consoleDir, root, executable string, args ...string) error {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return errors.New("no graphical session found (DISPLAY / WAYLAND_DISPLAY are not set)")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		return errors.New("sh was not found in PATH")
	}

	command := []string{"sh", "-c", unixRunner, "console", consoleDir,
		"env", EnvConsoleDir + "=" + consoleDir, EnvConsoleRoot + "=" + root, executable}
	command = append(command, args...)

	var failures []string
	for _, candidate := range orderedCandidates() {
		path, err := exec.LookPath(candidate.name)
		if err != nil {
			continue
		}
		err = startTerminalProcess(consoleDir, root, path, candidate.wrap(command))
		if err == nil {
			return nil
		}
		failures = append(failures, candidate.name+": "+err.Error())
	}
	if len(failures) > 0 {
		return fmt.Errorf("no terminal could be started (%s)", strings.Join(failures, "; "))
	}
	return errors.New("no supported terminal was found")
}

func startTerminalProcess(consoleDir, root, path string, argv []string) error {
	cmd := exec.Command(path, argv...)
	cmd.Dir = consoleDir
	cmd.Env = ChildEnv(consoleDir, root)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil

	done := make(chan error, 1)
	if err := StartDetached(cmd, done); err != nil {
		return err
	}
	select {
	case err := <-done:
		return err
	case <-time.After(terminalLaunchWait):
		return nil
	}
}

func openMacTerminal(consoleDir, root, executable string, args ...string) error {
	path, err := exec.LookPath("osascript")
	if err != nil {
		return errors.New("osascript not found")
	}
	var command strings.Builder
	command.WriteString("cd -- ")
	command.WriteString(shellQuote(consoleDir))
	command.WriteString(" && env ")
	command.WriteString(shellQuote(EnvConsoleDir + "=" + consoleDir))
	command.WriteString(" ")
	command.WriteString(shellQuote(EnvConsoleRoot + "=" + root))
	command.WriteString(" ")
	command.WriteString(shellQuote(executable))
	for _, arg := range args {
		command.WriteString(" ")
		command.WriteString(shellQuote(arg))
	}
	command.WriteString(`; status=$?; case "$status" in 0|129|130|143) osascript -e 'tell application "Terminal" to close front window' 2>/dev/null & sleep 0.1;; *) printf '\n[console] exited with status %s. Press [Enter] to close this window...' "$status"; read -r _;; esac`)

	script := `on run argv
  tell application "Terminal"
    activate
    do script (item 1 of argv)
  end tell
end run`

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, "-e", script, command.String()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
