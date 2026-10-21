package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"norvet_console/internal"
)

func (a *Console) ensureRuntime() error {
	if err := os.MkdirAll(a.logDir, 0700); err != nil {
		return fmt.Errorf("create log directory %s: %w", a.logDir, err)
	}
	if err := os.MkdirAll(a.runtimeDir, 0700); err != nil {
		return fmt.Errorf("create runtime directory %s: %w", a.runtimeDir, err)
	}
	if err := os.Chmod(a.runtimeDir, 0700); err != nil {
		return fmt.Errorf("secure runtime directory %s: %w", a.runtimeDir, err)
	}
	return nil
}

func (a *Console) acquireAPILock() (func(), error) {
	if err := a.ensureRuntime(); err != nil {
		return nil, err
	}
	lockDir := filepath.Join(a.runtimeDir, "lock.d")
	for range 25 {
		err := os.Mkdir(lockDir, 0700)
		if err == nil {
			if err := os.WriteFile(filepath.Join(lockDir, "pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0600); err != nil {
				_ = os.Remove(lockDir)
				return nil, fmt.Errorf("write API lock %s: %w", lockDir, err)
			}
			return func() {
				_ = os.Remove(filepath.Join(lockDir, "pid"))
				_ = os.Remove(lockDir)
				a.pruneRuntimeDir()
			}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("create API lock %s: %w", lockDir, err)
		}
		data, readErr := os.ReadFile(filepath.Join(lockDir, "pid"))
		holder, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if readErr == nil && parseErr == nil && !internal.ProcessAlive(holder) {
			_ = os.Remove(filepath.Join(lockDir, "pid"))
			_ = os.Remove(lockDir)
			continue
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil, fmt.Errorf("another start/stop operation is in progress (lock: %s)", lockDir)
}

func (a *Console) pruneRuntimeDir() {
	_ = os.Remove(a.runtimeDir)
}

func (a *Console) statePath() string {
	return filepath.Join(a.runtimeDir, "api.state")
}

func (a *Console) readAPIStatus() (apiStatus, error) {
	var state apiState
	data, err := os.ReadFile(a.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return apiStatus{}, nil
	}
	if err != nil {
		return apiStatus{}, fmt.Errorf("read API state: %w", err)
	}
	if err := json.Unmarshal(data, &state); err != nil || state.SupervisorPID <= 0 || state.ChildPID <= 0 || state.StartEpoch <= 0 {
		_ = os.Remove(a.statePath())
		a.pruneRuntimeDir()
		return apiStatus{}, nil
	}
	if internal.ProcessMatches(state.SupervisorPID, state.SupervisorKey) {
		return apiStatus{
			Running: true,
			State:   state,
			Uptime:  time.Since(time.Unix(state.StartEpoch, 0)),
		}, nil
	}
	a.writeUnexpectedExit(state.LogFile)
	_ = os.Remove(a.statePath())
	a.pruneRuntimeDir()
	return apiStatus{}, nil
}

func (a *Console) writeUnexpectedExit(logFile string) {
	if logFile == "" || logHasTermination(logFile) {
		return
	}

	file, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		_, _ = fmt.Fprintf(a.err, "Warning: cannot append unexpected-exit details to %s: %v\n", logFile, err)
		return
	}

	if _, err := fmt.Fprintf(file, "%s\n[API Console] Supervisor ended unexpectedly; termination time unknown.\n", logSeparator); err != nil {
		_, _ = fmt.Fprintf(a.err, "Warning: cannot append unexpected-exit details to %s: %v\n", logFile, err)
	}
	if err := file.Close(); err != nil {
		_, _ = fmt.Fprintf(a.err, "Warning: cannot close log file %s: %v\n", logFile, err)
	}
}

func (a *Console) printAPIStatus() {
	status, err := a.readAPIStatus()
	if err != nil {
		_, _ = fmt.Fprintln(a.err, err)
		return
	}

	if status.Running {
		_, _ = fmt.Fprintf(a.out, "Status: Running\nPID: %d\nUptime: %s\nLog: %s\n", status.State.ChildPID, formatDuration(status.Uptime), status.State.LogFile)
		return
	}
	_, _ = fmt.Fprintln(a.out, "Status: Offline")
}

func (a *Console) startAPI() error {
	if _, err := exec.LookPath("go"); err != nil {
		return errors.New("go was not found in PATH. Install Go (or fix PATH) and try again")
	}
	if !isDir(a.apiDir) {
		return fmt.Errorf("API directory not found: %s", a.apiDir)
	}
	release, err := a.acquireAPILock()
	if err != nil {
		return err
	}
	defer release()

	status, err := a.readAPIStatus()
	if err != nil {
		return err
	}
	if status.Running {
		if _, err := fmt.Fprintf(a.out, "API is already running (PID %d, uptime %s).\nLog: %s\n", status.State.ChildPID, formatDuration(status.Uptime), status.State.LogFile); err != nil {
			return fmt.Errorf("write API status: %w", err)
		}
		return nil
	}
	logFile, err := a.newLogFile()
	if err != nil {
		return err
	}
	started := time.Now()
	if err := appendLog(logFile, "[API Console] Started at: "+started.Local().Format("02:01:06 15:04:05"), "[API Console] Project root: "+a.root, logSeparator); err != nil {
		return fmt.Errorf("initialize log file: %w", err)
	}
	key, err := randomKey()
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintln(a.out, "\nStarting API..."); err != nil {
		return fmt.Errorf("write startup message: %w", err)
	}
	cmd := exec.Command(a.executable, "_supervise", logFile, strconv.FormatInt(started.Unix(), 10), started.Local().Format("02:01:06 15:04:05"), key)
	// nil stdio = /dev/null. io.Discard would create a pipe that breaks (EPIPE)
	// as soon as this process exits.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := internal.StartDetached(cmd, nil); err != nil {
		return fmt.Errorf("start API supervisor: %w", err)
	}

	for range 25 {
		status, err = a.readAPIStatus()
		if err != nil {
			return err
		}
		if status.Running {
			break
		}
		if !internal.ProcessAlive(cmd.Process.Pid) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !status.Running {
		_, _ = fmt.Fprintln(a.err, "API failed to start.")
		a.showLogTail(logFile)
		return errors.New("API failed to start")
	}
	for range 8 {
		time.Sleep(300 * time.Millisecond)
		status, err = a.readAPIStatus()
		if err != nil {
			return err
		}
		if !status.Running {
			_, _ = fmt.Fprintln(a.err, "API exited right after starting.")
			a.showLogTail(logFile)
			return errors.New("API exited right after starting")
		}
	}
	if _, err := fmt.Fprintf(a.out, "API is running (PID %d).\n\nLog: %s\n(API may still be compiling; follow the log to confirm it is serving.)\n", status.State.ChildPID, logFile); err != nil {
		return fmt.Errorf("write startup status: %w", err)
	}

	monitorKey, err := randomKey()
	if err == nil {
		if a.openTerminal != nil && a.openTerminal("api", "watch", "--attached", logFile, monitorKey) == nil {
			_, _ = fmt.Fprintln(a.out, "\nLive log opened in a new terminal.")
			return nil
		}
	}
	_, _ = fmt.Fprintln(a.out, "No GUI terminal available for the live log. Follow it with:")
	_, _ = fmt.Fprintf(a.out, "  %s api watch\n", a.executable)
	return nil
}

func (a *Console) stopAPI() error {
	release, err := a.acquireAPILock()
	if err != nil {
		return err
	}
	defer release()
	status, err := a.readAPIStatus()
	if err != nil {
		return err
	}
	if !status.Running {
		_, _ = fmt.Fprintln(a.out, "API is not running.")
		return nil
	}
	if _, err := fmt.Fprintf(a.out, "\nStopping API (PID %d)...\n", status.State.ChildPID); err != nil {
		return fmt.Errorf("write shutdown message: %w", err)
	}
	if err := internal.SignalProcess(status.State.SupervisorPID, syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("signal API supervisor: %w", err)
	}
	deadline := time.Now().Add(time.Duration(stopGraceSeconds())*time.Second + 12*time.Second)
	for time.Now().Before(deadline) {
		if !internal.ProcessAlive(status.State.SupervisorPID) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if internal.ProcessAlive(status.State.SupervisorPID) {
		_, _ = fmt.Fprintln(a.err, "Warning: supervisor did not exit in time; forcing termination.")
		_ = internal.TerminateChild(status.State.ChildPID, 0)
		_ = internal.SignalProcess(status.State.SupervisorPID, syscall.SIGKILL)
		time.Sleep(300 * time.Millisecond)
		_ = os.Remove(a.statePath())
	}
	elapsed := time.Since(time.Unix(status.State.StartEpoch, 0))
	if !logHasTermination(status.State.LogFile) {
		if err := appendLog(status.State.LogFile,
			logSeparator,
			"[API Console] Process force-killed by API Console.",
			"Terminated at: "+time.Now().Format("02:01:06 15:04:05"),
			fmt.Sprintf("Elapsed time: %d seconds", int64(elapsed.Seconds()))); err != nil {
			_, _ = fmt.Fprintf(a.err, "Warning: cannot append shutdown details to %s: %v\n", status.State.LogFile, err)
		}
	}
	if _, err := fmt.Fprintf(a.out, "API stopped. It ran for %s.\n\nLog: %s\n", formatDuration(elapsed), status.State.LogFile); err != nil {
		return fmt.Errorf("write shutdown status: %w", err)
	}
	return nil
}

func (a *Console) Supervise(args []string) (returnErr error) {
	if len(args) != 4 {
		return errors.New("invalid supervisor arguments")
	}
	startEpoch, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || startEpoch <= 0 || args[3] == "" {
		return errors.New("invalid supervisor state")
	}
	logFile, human, key := args[0], args[2], args[3]
	if err := a.ensureRuntime(); err != nil {
		return err
	}
	log, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if err := log.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("close API log: %w", err)
		}
	}()

	cmd := exec.Command("go", "run", ".")
	cmd.Dir = a.apiDir
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Stdin = nil
	if err := internal.ConfigureChild(cmd); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		_, _ = fmt.Fprintf(log, "[API Console] Could not start Go API: %v\n", err)
		return err
	}
	state := apiState{SupervisorPID: os.Getpid(), SupervisorKey: key, ChildPID: cmd.Process.Pid, StartEpoch: startEpoch, StartHuman: human, LogFile: logFile}
	if err := a.writeState(state); err != nil {
		_ = internal.TerminateChild(cmd.Process.Pid, time.Duration(stopGraceSeconds())*time.Second)
		_, _ = cmd.Process.Wait()
		return fmt.Errorf("publish API state: %w", err)
	}

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	signals := make(chan os.Signal, 2)
	internal.SignalNotify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer internal.SignalStop(signals)
	reason := "exited on its own"
	var childErr error
	for {
		select {
		case childErr = <-waitCh:
			goto finished
		case received := <-signals:
			_ = received
			reason = "stop requested via API Console"
			_ = internal.TerminateChild(cmd.Process.Pid, time.Duration(stopGraceSeconds())*time.Second)
			childErr = <-waitCh
			goto finished
		case <-time.After(500 * time.Millisecond):
			if monitor, monitorErr := a.readMonitor(); monitorErr == nil && monitor.PID > 0 && !internal.ProcessCommandMatches(monitor.PID, monitor.Key, "--attached") {
				reason = "log terminal closed"
				_ = internal.TerminateChild(cmd.Process.Pid, time.Duration(stopGraceSeconds())*time.Second)
				childErr = <-waitCh
				goto finished
			}
		}
	}

finished:
	exitCodeVal := 0
	if childErr != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](childErr); ok {
			exitCodeVal = exitErr.ExitCode()
		} else {
			exitCodeVal = 1
		}
	}
	elapsed := time.Now().Unix() - startEpoch
	_, _ = fmt.Fprintf(log, "%s\n[API Console] Process exit code: %d (%s)\nTerminated at: %s\nElapsed time: %d seconds\n",
		logSeparator, exitCodeVal, reason, time.Now().Format("02:01:06 15:04:05"), elapsed)
	_ = os.Remove(filepath.Join(a.runtimeDir, "monitor"))
	if current, readErr := a.readRawState(); readErr == nil && current.SupervisorPID == os.Getpid() && current.SupervisorKey == key {
		_ = os.Remove(a.statePath())
	}
	a.pruneRuntimeDir()
	return nil
}

func (a *Console) writeState(state apiState) error {
	if err := a.ensureRuntime(); err != nil {
		return err
	}
	return writeJSONAtomic(a.statePath(), state)
}

func (a *Console) readRawState() (apiState, error) {
	data, err := os.ReadFile(a.statePath())
	if err != nil {
		return apiState{}, err
	}
	var state apiState
	err = json.Unmarshal(data, &state)
	return state, err
}

func stopGraceSeconds() int {
	value, err := strconv.Atoi(os.Getenv("API_STOP_GRACE"))
	if err != nil || value < 0 {
		return 8
	}
	return value
}

func randomKey() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate process identity key: %w", err)
	}
	return hex.EncodeToString(bytes[:]), nil
}
