package prisma

import (
	"bufio"
	"fmt"
	"norvet_console/shared"
	"os"
	"path/filepath"
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
		root:      config.Root,
		prismaDir: config.PrismaDir,
		in:        reader,
		out:       output,
		err:       errorOutput,
	}
}

func (a *Console) validatePrismaProject() error {
	if !isDir(filepath.Join(a.root, "ops", "prisma")) {
		return fmt.Errorf("not a valid project root (missing ops/prisma): %s", a.root)
	}
	if !isDir(a.prismaDir) {
		return fmt.Errorf("prisma directory does not exist: %s", a.prismaDir)
	}
	return nil
}

func (a *Console) prompt(promptText string) (string, error) {
	return shared.Prompt(a.out, a.in, promptText)
}

/*
func (a *Console) pause() {
	shared.Pause(a.out, a.in)
}
*/

func (a *Console) report(err error) {
	if err != nil {
		_, err := fmt.Fprintln(a.err, err)
		if err != nil {
			return
		}
	}
}
