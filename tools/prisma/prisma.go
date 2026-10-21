package prisma

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func (a *Console) detectPrisma() prismaInfo {
	if _, err := exec.LookPath("npx"); err != nil {
		return prismaInfo{problem: "npx was not found in PATH. Install Node.js (which includes npx)."}
	}
	cli := filepath.Join(a.prismaDir, "node_modules", ".bin", "prisma")
	if runtime.GOOS == "windows" {
		cli += ".cmd"
	}
	if _, err := os.Stat(cli); err != nil {
		return prismaInfo{problem: fmt.Sprintf("Prisma CLI not found in %s/node_modules/.bin. Run 'npm install' there.", a.prismaDir)}
	}
	output, err := a.runPrismaCapture("--version")
	if err != nil {
		return prismaInfo{problem: "Could not read the Prisma version (is Node.js working?)."}
	}
	for line := range strings.SplitSeq(output, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == "prisma" {
			return prismaInfo{version: strings.TrimSpace(parts[1])}
		}
	}
	return prismaInfo{problem: "Could not read the Prisma version (is Node.js working?)."}
}

func (a *Console) runPrisma(info prismaInfo, args ...string) error {
	if info.version == "" {
		problem := info.problem
		if problem == "" {
			problem = "Prisma is not available."
		}
		return errors.New(problem)
	}
	_, err := fmt.Fprintf(a.out, "\n> npx prisma %s\n\n", strings.Join(args, " "))
	if err != nil {
		return err
	}
	cmd := a.prismaCommandExec(args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, a.out, a.err
	err = cmd.Run()
	_, _ = fmt.Fprintln(a.out)

	if err == nil {
		_, err := fmt.Fprintln(a.out, "Done.")
		if err != nil {
			return err
		}
		return nil
	}
	code := exitCode(err)
	if code == 130 {
		_, err := fmt.Fprintln(a.err, "Warning: Prisma stopped without making changes (exit status 130).")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(a.err, "  This can mean a prompt was declined or cancelled, or Prisma needs a database reset.")
		if err != nil {
			return err
		}
		return err
	}
	return fmt.Errorf("prisma exited with status %d", code)
}

func (a *Console) prismaCommandExec(args ...string) *exec.Cmd {
	npx, _ := exec.LookPath("npx")
	commandArgs := append([]string{"--no-install", "prisma"}, args...)
	cmd := exec.Command(npx, commandArgs...)
	cmd.Dir = a.prismaDir
	return cmd
}

func (a *Console) runPrismaCapture(args ...string) (string, error) {
	cmd := a.prismaCommandExec(args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func exitCode(err error) int {
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode()
	}
	return 1
}
