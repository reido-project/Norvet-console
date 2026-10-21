//go:build !windows

package internal

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// StartDetached starts cmd in its own session so it survives this process and
// its terminal. If done is non-nil (it must be buffered), the result of
// cmd.Wait is delivered there once the process ends.
func StartDetached(cmd *exec.Cmd, done chan<- error) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		err := cmd.Wait()
		if done != nil {
			done <- err
		}
	}()
	return nil
}

// OpenConsoleWindow is only needed on Windows; Unix opens terminals through
// terminal emulators (see terminal.go).
func OpenConsoleWindow(dir string, env []string, exe string, args []string, title string) error {
	return errors.New("OpenConsoleWindow is only supported on Windows")
}

// EnableVT reports whether the terminal understands ANSI/VT escape sequences.
// Unix terminals always do; nothing has to be switched on.
func EnableVT() bool { return true }

func ConfigureChild(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		if end := strings.LastIndexByte(string(data), ')'); end >= 0 {
			fields := strings.Fields(string(data)[end+1:])
			if len(fields) > 0 && fields[0] == "Z" {
				return false
			}
		} else {
			return false
		}
		return true
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func ProcessMatches(pid int, key string) bool {
	return ProcessCommandMatches(pid, key, "_supervise")
}

func ProcessCommandMatches(pid int, key, command string) bool {
	if key == "" || !ProcessAlive(pid) {
		return false
	}
	if data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline"); err == nil {
		return strings.Contains(string(data), key) && strings.Contains(string(data), command)
	}
	cmd := exec.Command("ps", "-o", "args=", "-p", strconv.Itoa(pid))
	data, err := cmd.Output()
	return err == nil && strings.Contains(string(data), key) && strings.Contains(string(data), command)
}

func SignalProcess(pid int, sig os.Signal) error {
	if pid <= 0 {
		return os.ErrProcessDone
	}
	signale, ok := sig.(syscall.Signal)
	if !ok {
		return errors.New("unsupported process signal")
	}
	if err := syscall.Kill(pid, signale); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}

func TerminateChild(pid int, grace time.Duration) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pid, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func SignalNotify(channel chan os.Signal, signals ...os.Signal) {
	signal.Notify(channel, signals...)
}

func SignalStop(channel chan os.Signal) {
	signal.Stop(channel)
}
