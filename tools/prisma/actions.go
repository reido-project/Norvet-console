package prisma

import (
	"errors"
	"fmt"
	"io"
	"norvet_console/shared"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

func (a *Console) resolveMigration(info prismaInfo) (bool, error) {
	shared.Clear()
	_, err := fmt.Fprintf(a.out, "%s\n    Resolve a Migration\n%s\n  1. Mark as applied      (--applied)\n  2. Mark as rolled back  (--rolled-back)\n  0. Cancel\n%s\n",
		separator, separator, separator)
	if err != nil {
		return false, err
	}
	choice, err := a.prompt("> Select: ")
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	mode := ""
	switch choice {
	case "1":
		mode = "--applied"
	case "2":
		mode = "--rolled-back"
	default:
		return true, nil
	}
	migrationsDir := filepath.Join(a.prismaDir, "prisma", "migrations")
	if entries, readErr := os.ReadDir(migrationsDir); readErr == nil {
		var migrations []string
		for _, entry := range entries {
			if entry.IsDir() {
				migrations = append(migrations, entry.Name())
			}
		}
		sort.Strings(migrations)
		if len(migrations) > 0 {
			_, err := fmt.Fprintf(a.out, "\nMigrations found in %s:\n", migrationsDir)
			if err != nil {
				return false, err
			}
			for _, migration := range migrations {
				_, err := fmt.Fprintf(a.out, "  %s\n", migration)
				if err != nil {
					return false, err
				}
			}
		}
	}
	_, err = fmt.Fprintln(a.out)
	if err != nil {
		return false, err
	}
	name, err := a.prompt("> Migration name (empty to cancel): ")
	if errors.Is(err, io.EOF) || name == "" {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(name) {
		return false, errors.New("invalid migration name. Use only letters, digits, '_' and '-' (the folder name)")
	}
	return false, a.runPrisma(info, "migrate", "resolve", mode, name)
}

func (a *Console) resetDatabase(info prismaInfo) (bool, error) {
	_, err := fmt.Fprintln(a.out, "Looking up the target database...")
	if err != nil {
		return false, err
	}
	output, err := a.runPrismaCapture("migrate", "status")
	target := ""
	if err == nil {
		for line := range strings.SplitSeq(stripANSI(output), "\n") {
			if strings.HasPrefix(line, "Datasource ") {
				target = line
				break
			}
		}
	}
	if target == "" {
		target = "(could not determine it - check DATABASE_URL in .env)"
	}
	_, err = fmt.Fprintln(a.out)
	if err != nil {
		return false, err
	}
	_, err = fmt.Fprintln(a.out, "This drops the database, re-applies every migration and runs the seeder.")
	if err != nil {
		return false, err
	}
	_, err = fmt.Fprint(a.out, "All data will be lost.\n\n")
	if err != nil {
		return false, err
	}
	_, err = fmt.Fprintf(a.out, "  Target: %s\n\n", target)
	if err != nil {
		return false, err
	}
	answer, err := a.prompt("> Type RESET to continue (anything else cancels): ")
	if err != nil {
		if errors.Is(err, io.EOF) {
			return true, nil
		}
		return true, err
	}
	if answer != "RESET" {
		_, err := fmt.Fprintln(a.out, "\nCancelled.")
		if err != nil {
			return false, err
		}
		time.Sleep(time.Second)
		return true, nil
	}
	return false, a.runPrisma(info, "migrate", "reset", "--force")
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func stripANSI(value string) string {
	var result strings.Builder
	inEscape := false
	for _, r := range value {
		if inEscape {
			if r >= '@' && r <= '~' {
				inEscape = false
			}
			continue
		}
		if r == '\x1b' {
			inEscape = true
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
