package api

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"norvet_console/internal"
	"norvet_console/shared"
)

func New(config Config) *Console {
	input := config.Input
	if input == nil {
		input = os.Stdin
	}
	output := config.Output
	if output == nil {
		output = os.Stdout
	}
	errorOutput := config.Error
	if errorOutput == nil {
		errorOutput = os.Stderr
	}
	reader, ok := input.(*bufio.Reader)
	if !ok {
		reader = bufio.NewReader(input)
	}
	return &Console{
		root:         config.Root,
		executable:   config.Executable,
		apiDir:       config.APIDir,
		logDir:       config.LogDir,
		runtimeDir:   config.RuntimeDir,
		in:           reader,
		out:          output,
		err:          errorOutput,
		openTerminal: config.OpenTerminal,
	}
}

func (a *Console) Command(args []string) error {
	command := "help"
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}
	switch command {
	case "start":
		return a.startAPI()
	case "stop":
		return a.stopAPI()
	case "status":
		a.printAPIStatus()
		return nil
	case "watch":
		attached := false
		if len(args) > 0 && args[0] == "--attached" {
			attached = true
			args = args[1:]
		}
		logFile, monitorKey := "", ""
		if len(args) > 0 {
			logFile = args[0]
		}
		if len(args) > 1 {
			monitorKey = args[1]
		}
		return a.watchLog(logFile, attached, monitorKey)
	case "help", "-h", "--help":
		_, _ = fmt.Fprintln(a.out, "Usage: console api start|stop|status|watch [logfile]")
		return nil
	default:
		return fmt.Errorf("unknown API command %q", command)
	}
}

func (a *Console) Menu() error {
	for {
		status, choice, err := a.menuChoice()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch choice {
		case "1":
			if status.Running {
				err = a.stopAPI()
			} else {
				err = a.startAPI()
			}
			a.report(err)
			time.Sleep(3 * time.Second)
		case "2":
			_, _ = fmt.Fprintln(a.out, "\nUnavailable.")
			time.Sleep(1500 * time.Millisecond)
		case "3":
			_, _ = fmt.Fprintln(a.out)
			a.report(a.Compile())
			time.Sleep(3 * time.Second)
		case "4":
			_, _ = fmt.Fprintln(a.out, "\nUnavailable.")
			time.Sleep(1500 * time.Millisecond)
		case "0":
			_, _ = fmt.Fprintln(a.out, "\nExiting... (the API keeps running if it was started)")
			return nil
		case "":
		default:
			_, _ = fmt.Fprintln(a.out, "\nInvalid option.")
			time.Sleep(time.Second)
		}
	}
}

const menuPrompt = "Choose an option: "

// drawMenu paints the whole menu and returns the screen row (1-based) of the
// "Uptime" line, or 0 when the API is offline and there is no such line.
func (a *Console) drawMenu(status apiStatus) int {
	a.clear()
	row := 0
	_, _ = fmt.Fprintf(a.out, "%s\n            API Console\n%s\n", separator, separator)
	row += 3
	if status.Running {
		_, _ = fmt.Fprintf(a.out, "  Status: Running\n  PID: %d\n", status.State.ChildPID)
		row += 3
		_, _ = fmt.Fprintf(a.out, "  Uptime: %s\n", formatDuration(status.Uptime))
	} else {
		_, _ = fmt.Fprintln(a.out, "  Status: Offline")
		row = 0
	}
	_, _ = fmt.Fprintf(a.out, "\n  1. Start / Stop API\n  2. Run Test Series [Unavailable]\n  3. Compile\n  4. Push to VPS [Unavailable]\n  0. Exit\n%s\n", separator)
	return row
}

type lineResult struct {
	text string
	err  error
}

// menuChoice draws the menu, waits for the user's choice and, while it waits,
// keeps the uptime counter ticking and notices when the API starts or stops
// behind the menu's back. It returns the status the menu last displayed.
func (a *Console) menuChoice() (apiStatus, string, error) {
	status, err := a.readAPIStatus()
	if err != nil {
		return status, "", err
	}
	uptimeRow := a.drawMenu(status)
	_, _ = fmt.Fprint(a.out, menuPrompt)

	// Live updates need cursor addressing; fall back to a plain blocking
	// prompt when the output is piped or the terminal cannot do it.
	if !a.interactive() {
		text, err := shared.ReadLine(a.in)
		return status, text, err
	}

	input := make(chan lineResult, 1)
	go func() {
		text, err := shared.ReadLine(a.in)
		input <- lineResult{text, err}
	}()

	// 250ms keeps the counter from visibly skipping a second.
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	shown := formatDuration(status.Uptime)
	lastCheck := time.Now()

	for {
		select {
		case result := <-input:
			return status, result.text, result.err
		case now := <-ticker.C:
			if now.Sub(lastCheck) >= 5*time.Second {
				lastCheck = now
				if fresh, err := a.readAPIStatus(); err == nil && statusChanged(status, fresh) {
					status = fresh
					uptimeRow = a.drawMenu(status)
					_, _ = fmt.Fprint(a.out, menuPrompt)
					shown = formatDuration(status.Uptime)
					continue
				}
			}
			if !status.Running || uptimeRow == 0 {
				continue
			}
			text := formatDuration(time.Since(time.Unix(status.State.StartEpoch, 0)))
			if text == shown {
				continue
			}
			shown = text
			// Save cursor, repaint only the uptime line, restore cursor, so
			// whatever the user is typing at the prompt is left untouched.
			_, _ = fmt.Fprintf(a.out, "\0337\033[%d;1H\033[2K  Uptime: %s\0338", uptimeRow, text)
		}
	}
}

func statusChanged(old, fresh apiStatus) bool {
	return old.Running != fresh.Running ||
		old.State.ChildPID != fresh.State.ChildPID ||
		old.State.StartEpoch != fresh.State.StartEpoch
}

// interactive reports whether the menu may repaint itself in place.
func (a *Console) interactive() bool {
	return isTerminal(os.Stdout) && os.Getenv("TERM") != "dumb" && internal.EnableVT()
}

func (a *Console) report(err error) {
	if err != nil {
		_, _ = fmt.Fprintln(a.err, err)
	}
}

func (a *Console) prompt(prompt string) (string, error) {
	return shared.Prompt(a.out, a.in, prompt)
}

/*
func (a *Console) pause() {
	shared.Pause(a.out, a.in)
}
*/

func (a *Console) clear() {
	if a.interactive() {
		_, _ = fmt.Fprint(a.out, "\033[H\033[2J")
	}
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func formatDuration(value time.Duration) string {
	seconds := max(int64(value.Seconds()), 0)
	return fmt.Sprintf("%02d:%02d:%02d", seconds/3600, seconds%3600/60, seconds%60)
}
