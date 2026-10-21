package shared

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/inancgumus/screen"
)

func Prompt(out io.Writer, in *bufio.Reader, prompt string) (string, error) {
	_, err := fmt.Fprint(out, prompt)
	if err != nil {
		return "", err
	}
	return ReadLine(in)
}

// ReadLine reads one line from in without printing a prompt. It returns
// io.EOF when the input is exhausted before any text was read.
func ReadLine(in *bufio.Reader) (string, error) {
	line, err := in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if errors.Is(err, io.EOF) && line == "" {
		return "", io.EOF
	}
	return strings.TrimSpace(strings.TrimSuffix(line, "\r")), nil
}

/*
func Pause(out io.Writer, in *bufio.Reader) {
	_, _ = Prompt(out, in, "Press [Enter] to continue...")
}
*/

func Clear() {
	screen.Clear()
	screen.MoveTopLeft()
}
