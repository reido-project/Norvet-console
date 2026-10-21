//go:build windows

package internal

import (
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

const (
	createNewConsole      = 0x00000010
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
)

// StartDetached starts cmd without a visible console, in its own process group,
// so it survives this process and its console window. If done is non-nil (it
// must be buffered), the result of cmd.Wait is delivered there.
func StartDetached(cmd *exec.Cmd, done chan<- error) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | createNoWindow,
		HideWindow:    true,
	}
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

// OpenConsoleWindow runs exe in a brand new console window.
//
// Starting the executable directly with CREATE_NEW_CONSOLE is not enough: Go
// wires a nil stdin/stdout/stderr to NUL, so the new window would be dead.
// `start` creates the window and gives the program the real console handles.
func OpenConsoleWindow(dir string, env []string, exe string, args []string, title string) error {
	line := "cmd.exe /d /c start " + syscall.EscapeArg(title) + " " + syscall.EscapeArg(exe)
	for _, arg := range args {
		line += " " + syscall.EscapeArg(arg)
	}
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line, CreationFlags: createNoWindow}
	cmd.Dir = dir
	cmd.Env = env
	return cmd.Run()
}

// EnableVT switches the console attached to stdout into virtual-terminal mode
// so ANSI escape sequences (cursor positioning, clearing) are interpreted
// instead of printed. It returns false when stdout is not a console or the
// console is too old to support it.
func EnableVT() bool {
	handle := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	return windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}

// ConfigureChild keeps the child out of this console's Ctrl+C / Ctrl+Break
// delivery and gives it no window of its own; it is stopped with taskkill.
func ConfigureChild(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | createNoWindow,
		HideWindow:    true,
	}
	return nil
}

func ProcessAlive(pid int) bool {
	output, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && fields[1] == strconv.Itoa(pid) {
			return true
		}
	}
	return false
}

func ProcessMatches(pid int, key string) bool {
	return ProcessCommandMatches(pid, key, "_supervise")
}

func ProcessCommandMatches(pid int, key, command string) bool {
	if key == "" || !ProcessAlive(pid) {
		return false
	}
	filter := "ProcessId=" + strconv.Itoa(pid)
	output, err := exec.Command("powershell", "-NoProfile", "-Command", "(Get-CimInstance Win32_Process -Filter '"+filter+"').CommandLine").Output()
	return err == nil && strings.Contains(string(output), key) && strings.Contains(string(output), command)
}

func SignalProcess(pid int, sig os.Signal) error {
	// The supervisor has no window, so a plain taskkill (WM_CLOSE) is ignored;
	// always force it. /T also takes down the API process tree.
	_ = sig
	return exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
}

func TerminateChild(pid int, grace time.Duration) error {
	_ = exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T").Run()
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) && ProcessAlive(pid) {
		time.Sleep(200 * time.Millisecond)
	}
	if ProcessAlive(pid) {
		return exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
	}
	return nil
}

func SignalNotify(channel chan os.Signal, signals ...os.Signal) {
	signal.Notify(channel, signals...)
}

func SignalStop(channel chan os.Signal) {
	signal.Stop(channel)
}
