package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"norvet_console/internal"
	"norvet_console/shared"
	"os"
	"path/filepath"
	"strings"
	"time"

	apiconsole "norvet_console/tools/api"
	prismaconsole "norvet_console/tools/prisma"
)

const separator = "——————————————————————————————————"

type app struct {
	root       string
	consoleDir string
	executable string
	apiDir     string
	prismaDir  string
	logDir     string
	runtimeDir string
	in         *bufio.Reader
	out        io.Writer
	err        io.Writer
	apiTool    *apiconsole.Console
	prismaTool *prismaconsole.Console
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, err := fmt.Fprintln(os.Stderr, "Error:", err)
		if err != nil {
			return
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	executable = strings.TrimSuffix(executable, " (deleted)")
	executable, err = filepath.Abs(executable)
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	consoleDir := filepath.Dir(executable)
	if isTempBuild(executable) {
		if cwd, cwdErr := os.Getwd(); cwdErr == nil {
			consoleDir = cwd
		}
	}
	if inherited := os.Getenv(internal.EnvConsoleDir); inherited != "" && isDir(inherited) {
		consoleDir = inherited
	}
	root := os.Getenv(internal.EnvConsoleRoot)
	if root == "" || !isDir(root) {
		root, err = resolveProjectRoot(consoleDir)
		if err != nil {
			return err
		}
	}
	_ = os.Setenv(internal.EnvConsoleDir, consoleDir)
	_ = os.Setenv(internal.EnvConsoleRoot, root)
	a := &app{
		root:       root,
		consoleDir: consoleDir,
		executable: executable,
		apiDir:     filepath.Join(root, "api"),
		prismaDir:  filepath.Join(root, "external", "nodejs"),
		logDir:     filepath.Join(consoleDir, "logs", "api"),
		runtimeDir: filepath.Join(consoleDir, "logs", "api", ".runtime"),
		in:         bufio.NewReader(os.Stdin),
		out:        os.Stdout,
		err:        os.Stderr,
	}
	a.apiTool = apiconsole.New(apiconsole.Config{
		Root: a.root, Executable: a.executable, APIDir: a.apiDir,
		LogDir: a.logDir, RuntimeDir: a.runtimeDir, Input: a.in, Output: a.out, Error: a.err,
		OpenTerminal: func(args ...string) error {
			return a.openInNewTerminal(append([]string{"terminal"}, args...)...)
		},
	})
	a.prismaTool = prismaconsole.New(prismaconsole.Config{
		Root: a.root, PrismaDir: a.prismaDir, Input: a.in, Output: a.out, Error: a.err,
	})
	if len(args) > 0 && args[0] == "_supervise" {
		return a.apiTool.Supervise(args[1:])
	}
	return a.dispatch(args)
}

func isTempBuild(executable string) bool {
	if strings.Contains(executable, string(filepath.Separator)+"go-build") {
		return true
	}
	tmp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		tmp = os.TempDir()
	}
	reale, err := filepath.EvalSymlinks(executable)
	if err != nil {
		reale = executable
	}
	rel, err := filepath.Rel(tmp, reale)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}

func resolveProjectRoot(consoleDir string) (string, error) {
	for _, key := range []string{"API_PROJECT_ROOT", "PROJECT_ROOT"} {
		if value := os.Getenv(key); value != "" {
			root, err := filepath.Abs(value)
			if err != nil {
				return "", fmt.Errorf("resolve %s: %w", key, err)
			}
			valid := looksLikeRoot(root)
			if key == "PROJECT_ROOT" {
				valid = isDir(filepath.Join(root, "ops", "prisma"))
			}
			if !valid {
				if key == "PROJECT_ROOT" {
					return "", fmt.Errorf("%s does not contain ops/prisma/: %s", key, root)
				}
				return "", fmt.Errorf("%s does not contain api/ and ops/api/: %s", key, root)
			}
			return root, nil
		}
	}

	seen := make(map[string]bool)
	for _, base := range []string{consoleDir, mustGetwd()} {
		for dir := base; dir != "" && !seen[dir]; dir = filepath.Dir(dir) {
			seen[dir] = true
			if looksLikeRoot(dir) || isDir(filepath.Join(dir, "ops", "prisma")) {
				return dir, nil
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	return filepath.Dir(consoleDir), nil
}

func mustGetwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

func looksLikeRoot(dir string) bool {
	return isDir(filepath.Join(dir, "api")) && isDir(filepath.Join(dir, "ops", "api"))
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func (a *app) dispatch(args []string) error {
	if len(args) == 0 {
		return a.mainMenu()
	}
	switch args[0] {
	case "menu":
		if len(args) < 2 {
			return a.mainMenu()
		}
		switch args[1] {
		case "api":
			return a.apiTool.Menu()
		case "prisma":
			return a.prismaTool.Menu()
		case "git":
			return nil
		default:
			return fmt.Errorf("unknown menu %q", args[1])
		}
	case "api":
		return a.apiTool.Command(args[1:])
	case "build":
		return a.apiTool.Compile()
	case "prisma":
		return a.prismaTool.Command(args[1:])
	case "terminal":
		return a.terminalRun(args[1:])
	case "help", "-h", "--help":
		a.usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (a *app) usage() {
	_, err := fmt.Fprintln(a.out, `Usage: console [command]

  (no command)       Open the main menu
  menu api|prisma     Open a submenu directly
  api start|stop|status|watch
  build               Compile the API for the configured target
  prisma <arguments>  Run the installed local Prisma CLI`)
	if err != nil {
		return
	}
}

func (a *app) prompt(promptText string) (string, error) {
	return shared.Prompt(a.out, a.in, promptText)
}

/*
func (a *app) pause() {
	shared.Pause(a.out, a.in)
}
*/

func (a *app) openInNewTerminal(args ...string) error {
	return internal.OpenInNewTerminal(a.consoleDir, a.root, a.executable, args...)
}

func (a *app) mainMenu() error {
	for {
		shared.Clear()
		_, err := fmt.Fprintf(a.out, "%s\n    Select a program to start\n%s\n  1. API\n  2. Migration & Seeder\n  3. Git\n  0. Exit\n%s\n", separator, separator, separator)
		if err != nil {
			return err
		}
		choice, err := a.prompt("> Select: ")
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		targets := map[string]string{"1": "api", "2": "prisma", "3": "git"}
		name, ok := targets[choice]
		if choice == "0" {
			_, err := fmt.Fprintln(a.out, "\nExiting...")
			if err != nil {
				return err
			}
			return nil
		}
		if !ok {
			_, err := fmt.Fprintln(a.out, "\nInvalid option.")
			if err != nil {
				return err
			}
			time.Sleep(1500 * time.Millisecond)
			continue
		}
		switch name {
		case "api":
			_, err := fmt.Fprintln(a.out, "\nStarting API...")
			if err != nil {
				return err
			}
		case "prisma":
			_, err := fmt.Fprintln(a.out, "\nRunning Migration & Seeder...")
			if err != nil {
				return err
			}
		case "git":
			_, err := fmt.Fprintln(a.out, "\nOpening Git commands...")
			if err != nil {
				return err
			}
		}
		newTerminal, promptErr := a.prompt("Start in new terminal? (y/n): ")
		if promptErr != nil && !errors.Is(promptErr, io.EOF) {
			return promptErr
		}
		if strings.EqualFold(newTerminal, "y") || strings.EqualFold(newTerminal, "yes") {
			if launchErr := a.openInNewTerminal("terminal", "menu", name); launchErr != nil {
				_, err := fmt.Fprintf(a.err, "Failed to launch terminal: %v\n", launchErr)
				time.Sleep(3 * time.Second)
				if err != nil {
					return err
				}
			}
			continue
		}
		_, err = fmt.Fprint(a.out, "\nExecuting in current terminal...\n\n")
		if err != nil {
			return err
		}
		var runErr error
		switch name {
		case "api":
			runErr = a.apiTool.Menu()
		case "prisma":
			runErr = a.prismaTool.Menu()
		case "git":
		}
		if runErr != nil {
			_, err := fmt.Fprintf(a.err, "%v\n", runErr)
			if err != nil {
				return err
			}
		}
		_, err = fmt.Fprintf(a.out, "\nScript exited with status %d.\n", errorStatus(runErr))
		if err != nil {
			return err
		}
	}
}

func (a *app) terminalRun(args []string) error {
	runErr := a.dispatch(args)
	if runErr == nil && len(args) >= 2 && args[0] == "api" && args[1] == "watch" {
		// The log viewer already waits for the final log lines; close its
		// window right away once it is done (e.g. after Ctrl+C).
		return nil
	}
	if runErr != nil {
		_, err := fmt.Fprintln(a.err, runErr)
		if err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(a.out, "\nScript exited with status %d.\n", errorStatus(runErr))
	if err != nil {
		return err
	}
	time.Sleep(3 * time.Second)
	return nil
}

func errorStatus(err error) int {
	if err == nil {
		return 0
	}
	return 1
}
