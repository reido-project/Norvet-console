package api

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func (a *Console) Compile() error {
	goPath, err := exec.LookPath("go")
	if err != nil {
		return errors.New("go was not found in PATH. Install Go (or fix PATH) and try again")
	}
	if !isDir(a.apiDir) {
		return fmt.Errorf("API directory not found: %s", a.apiDir)
	}
	if _, err := os.Stat(filepath.Join(a.apiDir, "go.mod")); err != nil {
		return fmt.Errorf("go.mod not found in %s", a.apiDir)
	}
	envFile := filepath.Join(a.root, ".env")
	targetOS, sourceOS, err := resolveBuildTarget(os.Getenv("GOOS"), "GOOS", "OS", "GOOS", envFile, goPath)
	if err != nil {
		return err
	}
	targetArch, sourceArch, err := resolveBuildTarget(os.Getenv("GOARCH"), "GOARCH", "ARCH", "GOARCH", envFile, goPath)
	if err != nil {
		return err
	}
	supported, supportErr := commandOutput(goPath, "tool", "dist", "list")
	if supportErr == nil && strings.TrimSpace(supported) != "" {
		if !containsLine(supported, targetOS+"/"+targetArch) {
			return fmt.Errorf("go does not support target %s/%s (OS from %s, arch from %s); see go tool dist list", targetOS, targetArch, sourceOS, sourceArch)
		}
	}
	name := "api-" + targetOS + "-" + targetArch
	if targetOS == "windows" {
		name += ".exe"
	}
	binDir := filepath.Join(a.root, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fmt.Errorf("create output directory %s: %w", binDir, err)
	}
	temp, err := os.CreateTemp(binDir, "."+name+".tmp-")
	if err != nil {
		return fmt.Errorf("create temporary build output: %w", err)
	}
	tempPath := temp.Name()
	_ = temp.Close()
	_ = os.Remove(tempPath)
	defer func(name string) {
		_ = os.Remove(name)
	}(tempPath)

	hostOS, _ := commandOutput(goPath, "env", "GOHOSTOS")
	hostArch, _ := commandOutput(goPath, "env", "GOHOSTARCH")

	_, _ = fmt.Fprintf(a.out, "Source : %s\nTarget : %s/%s   (OS: %s, arch: %s)\n", a.apiDir, targetOS, targetArch, sourceOS, sourceArch)
	if strings.TrimSpace(hostOS) != targetOS || strings.TrimSpace(hostArch) != targetArch {
		_, _ = fmt.Fprintf(a.out, "Note   : cross-compiling from %s/%s\n", strings.TrimSpace(hostOS), strings.TrimSpace(hostArch))
	}
	output := filepath.Join(binDir, name)
	_, _ = fmt.Fprintf(a.out, "Output : %s\n\n", output)

	cmd := exec.Command(goPath, "build", "-o", tempPath, ".")
	cmd.Dir = a.apiDir
	cmd.Env = replaceEnv(os.Environ(), "GOOS", targetOS)
	cmd.Env = replaceEnv(cmd.Env, "GOARCH", targetArch)
	cmd.Stdout, cmd.Stderr = a.out, a.err
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build failed (exit code %d); any previous binary was left untouched", exitCode(err))
	}
	if err := os.Rename(tempPath, output); err != nil {
		return fmt.Errorf("could not move new binary into place: %w", err)
	}

	_, _ = fmt.Fprintf(a.out, "Build succeeded: %s\n", output)
	return nil
}

func resolveBuildTarget(override, label, envKey, goName, envFile, goPath string) (string, string, error) {
	value, source := override, label+" env var"
	if value == "" {
		if parsed, ok, err := envFileValue(envFile, envKey); err != nil {
			return "", "", err
		} else if ok && parsed != "" {
			value, source = parsed, ".env ("+envKey+")"
		}
	}
	if value == "" {
		var err error
		value, err = commandOutput(goPath, "env", goName)
		if err != nil {
			return "", "", fmt.Errorf("resolve %s using go env: %w", label, err)
		}
		value, source = strings.TrimSpace(value), "local Go"
	}
	if value == "" || !regexp.MustCompile(`^[a-z0-9]+$`).MatchString(value) {
		return "", "", fmt.Errorf("invalid %s value %q (from %s); expected lowercase letters/digits", label, value, source)
	}
	return value, source, nil
}

func commandOutput(path string, args ...string) (string, error) {
	cmd := exec.Command(path, args...)
	out, err := cmd.Output()
	return string(out), err
}

func containsLine(haystack, needle string) bool {
	for line := range strings.SplitSeq(haystack, "\n") {
		if strings.TrimSpace(line) == needle {
			return true
		}
	}
	return false
}

func envFileValue(path, key string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", path, err)
	}
	var result string
	found := false
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if after, ok := strings.CutPrefix(line, "export "); ok {
			line = strings.TrimSpace(after)
		}
		keyPart, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(keyPart) != key {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) > 0 && (value[0] == '"' || value[0] == '\'') {
			quote := value[0]
			if end := strings.IndexByte(value[1:], quote); end >= 0 {
				value = value[1 : end+1]
			} else {
				value = value[1:]
			}
		} else {
			for i, r := range value {
				if r == '#' && i > 0 && (value[i-1] == ' ' || value[i-1] == '\t') {
					value = strings.TrimSpace(value[:i])
					break
				}
			}
		}
		result, found = value, true
	}
	return result, found, nil
}

func replaceEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(item, prefix) {
			out = append(out, item)
		}
	}
	return append(out, prefix+value)
}

func exitCode(err error) int {
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode()
	}
	return 1
}
