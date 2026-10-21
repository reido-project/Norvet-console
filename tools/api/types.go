package api

import (
	"bufio"
	"io"
	"time"
)

const separator = "——————————————————————————————————"
const logSeparator = "--------------------------------------------------"

type Config struct {
	Root         string
	Executable   string
	APIDir       string
	LogDir       string
	RuntimeDir   string
	Input        io.Reader
	Output       io.Writer
	Error        io.Writer
	OpenTerminal func(...string) error
}

type Console struct {
	root         string
	executable   string
	apiDir       string
	logDir       string
	runtimeDir   string
	in           *bufio.Reader
	out          io.Writer
	err          io.Writer
	openTerminal func(...string) error
}

type apiState struct {
	SupervisorPID int    `json:"supervisor_pid"`
	SupervisorKey string `json:"supervisor_key"`
	ChildPID      int    `json:"child_pid"`
	StartEpoch    int64  `json:"start_epoch"`
	StartHuman    string `json:"start_human"`
	LogFile       string `json:"log_file"`
}

type apiStatus struct {
	Running bool
	State   apiState
	Uptime  time.Duration
}
