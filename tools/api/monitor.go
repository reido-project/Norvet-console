package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"norvet_console/internal"
)

type monitorState struct {
	PID int    `json:"pid"`
	Key string `json:"key"`
}

func appendLog(path string, lines ...string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func(file *os.File) {
		_ = file.Close()
	}(file)

	for _, line := range lines {
		if _, err := fmt.Fprintln(file, line); err != nil {
			return err
		}
	}
	return nil
}

func (a *Console) showLogTail(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}

	_, _ = fmt.Fprintf(a.out, "---- last lines of %s ----\n", path)
	for _, line := range lines {
		_, _ = fmt.Fprintln(a.out, line)
	}
	_, _ = fmt.Fprintln(a.out, "---------------------------")
}

func (a *Console) monitorPath() string {
	return filepath.Join(a.runtimeDir, "monitor")
}

func (a *Console) readMonitor() (monitorState, error) {
	data, err := os.ReadFile(a.monitorPath())
	if err != nil {
		return monitorState{}, err
	}

	var monitor monitorState
	if err := json.Unmarshal(data, &monitor); err != nil {
		return monitorState{}, err
	}
	return monitor, nil
}

func (a *Console) writeMonitor(monitor monitorState) error {
	return writeJSONAtomic(a.monitorPath(), monitor)
}

func (a *Console) watchLog(logFile string, attached bool, monitorKey string) error {
	if logFile == "" {
		status, err := a.readAPIStatus()
		if err != nil {
			return err
		}
		if status.Running {
			logFile = status.State.LogFile
		} else {
			logFile = latestLog(a.logDir)
		}
	}
	if logFile == "" {
		return fmt.Errorf("no log file found in %s", a.logDir)
	}
	if !filepath.IsAbs(logFile) {
		var err error
		logFile, err = filepath.Abs(logFile)
		if err != nil {
			return fmt.Errorf("resolve log path: %w", err)
		}
	}
	if _, err := os.Stat(logFile); err != nil {
		return fmt.Errorf("log file not found: %s: %w", logFile, err)
	}

	if isTerminal(os.Stdout) {
		_, _ = fmt.Fprint(a.out, "\033]10;#ffffff\007\033[97m")
		defer func() { _, _ = fmt.Fprint(a.out, "\033[0m\033]110\007") }()
	}
	_, _ = fmt.Fprintf(a.out, "API Console - live log: %s\n", logFile)
	if attached {
		_, _ = fmt.Fprintln(a.out, "(Ctrl+C or closing this window stops the API.)")
	} else {
		_, _ = fmt.Fprintln(a.out, "(Read-only view: Ctrl+C or closing this window does not stop the API.)")
	}
	_, _ = fmt.Fprintln(a.out, logSeparator)

	if attached {
		if monitorKey == "" {
			return errors.New("attached log viewer requires a monitor key")
		}
		if err := a.writeMonitor(monitorState{PID: os.Getpid(), Key: monitorKey}); err != nil {
			return fmt.Errorf("register live log viewer: %w", err)
		}
		defer func() {
			current, err := a.readMonitor()
			if err == nil && current.PID == os.Getpid() && current.Key == monitorKey {
				_ = os.Remove(a.monitorPath())
			}
		}()
	}

	return followFile(logFile, os.Stdout, attached, a)
}

func followFile(path string, out io.Writer, attached bool, a *Console) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func(file *os.File) {
		_ = file.Close()
	}(file)

	var offset int64
	// Ctrl+C, terminate and terminal hang-up are handled in both modes so the
	// viewer always returns normally; that lets its window be closed at once
	// instead of the process dying with a signal status.
	signals := make(chan os.Signal, 4)
	internal.SignalNotify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer internal.SignalStop(signals)

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	var stopDeadline time.Time // set once this viewer asked the API to stop

	for {
		n, copyErr := io.Copy(out, file)
		offset += n
		if copyErr != nil {
			return copyErr
		}

		if attached {
			status, statusErr := a.readAPIStatus()
			if statusErr != nil {
				return statusErr
			}
			if !status.Running || status.State.LogFile != path {
				time.Sleep(time.Second)
				if _, err := io.Copy(out, file); err != nil {
					return fmt.Errorf("flush remaining log output: %w", err)
				}
				return nil
			}
			if !stopDeadline.IsZero() && time.Now().After(stopDeadline) {
				// The supervisor ignored the stop request; do not leave the
				// window hanging forever.
				_, _ = fmt.Fprintln(out, "\n[API Console] API did not stop in time; forcing termination.")
				_ = internal.TerminateChild(status.State.ChildPID, 0)
				_ = internal.SignalProcess(status.State.SupervisorPID, syscall.SIGKILL)
				return nil
			}
		}

		select {
		case <-ticker.C:
			info, err := file.Stat()
			if err != nil {
				return fmt.Errorf("stat log file: %w", err)
			}
			if info.Size() < offset {
				if _, err := file.Seek(0, io.SeekStart); err != nil {
					return fmt.Errorf("rewind log file: %w", err)
				}
				offset = 0
			}
		case <-signals:
			if !attached {
				_, _ = fmt.Fprintln(out, "\n[API Console] Closing log viewer (the API keeps running).")
				return nil
			}
			if !stopDeadline.IsZero() {
				continue // already stopping; keep waiting for the supervisor
			}
			status, err := a.readAPIStatus()
			if err != nil {
				return fmt.Errorf("check API status after interrupt: %w", err)
			}
			if !status.Running || status.State.LogFile != path {
				return nil
			}
			_, _ = fmt.Fprintln(out, "\n[API Console] Interrupt received - stopping API...")
			stopDeadline = time.Now().Add(time.Duration(stopGraceSeconds())*time.Second + 12*time.Second)
			if err := internal.SignalProcess(status.State.SupervisorPID, syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return fmt.Errorf("stop API supervisor: %w", err)
			}
		}
	}
}

func latestLog(root string) string {
	var files []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() && (entry.Name() == ".runtime" || strings.HasPrefix(entry.Name(), ".")) {
			return filepath.SkipDir
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".log") {
			files = append(files, path)
		}
		return nil
	})

	sort.Slice(files, func(i, j int) bool {
		left, leftErr := os.Stat(files[i])
		right, rightErr := os.Stat(files[j])
		if leftErr != nil || rightErr != nil {
			return files[i] > files[j]
		}
		return left.ModTime().After(right.ModTime())
	})

	if len(files) == 0 {
		return ""
	}
	return files[0]
}

func logHasTermination(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(data), "\nTerminated at:")
}

func (a *Console) newLogFile() (string, error) {
	now := time.Now().Local()
	dateDir := now.Format("Jan_2") + ordinal(now.Day()) + "_" + now.Format("2006")
	dir := filepath.Join(a.logDir, dateDir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create log directory %s: %w", dir, err)
	}

	base := now.Format("15:04:05")
	for suffix := 0; suffix <= 99; suffix++ {
		name := base
		if suffix > 0 {
			name += "_" + strconv.Itoa(suffix)
		}
		path := filepath.Join(dir, name+".log")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("create log file %s: %w", path, err)
		}
		if err := file.Close(); err != nil {
			return "", fmt.Errorf("close log file %s: %w", path, err)
		}
		return path, nil
	}
	return "", fmt.Errorf("could not create a unique log file under %s", dir)
}

func ordinal(day int) string {
	if day%100 >= 11 && day%100 <= 13 {
		return "th"
	}
	switch day % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	default:
		return "th"
	}
}
