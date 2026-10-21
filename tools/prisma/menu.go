package prisma

import (
	"errors"
	"fmt"
	"io"
	"norvet_console/shared"
	"time"
)

func (a *Console) Menu() error {
	if err := a.validatePrismaProject(); err != nil {
		return err
	}
	info := a.detectPrisma()
	for {
		shared.Clear()
		_, err := fmt.Fprintf(a.out, "%s\n    Prisma Console\n    Prisma Version %s\n%s\n  1. Migration\n  2. Seed Database\n  3. Format, Validate & Generate Prisma Client\n  0. Exit\n%s\n",
			separator, valueOr(info.version, "unknown"), separator, separator)
		if err != nil {
			return err
		}
		if info.version == "" {
			_, err := fmt.Fprintln(a.out, info.problem)
			if err != nil {
				return err
			}
		}
		choice, err := a.prompt("> Select: ")
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch choice {
		case "1":
			_, err := fmt.Fprintln(a.out, "\nOpening Migration Console...")
			if err != nil {
				return err
			}
			if err := a.migrationMenu(info); err != nil {
				a.report(err)
			}
		case "2":
			_, err := fmt.Fprintln(a.out, "\nRunning Seeder...")
			if err != nil {
				return err
			}
			a.report(a.runPrisma(info, "db", "seed"))
			time.Sleep(3 * time.Second)
		case "3":
			_, err := fmt.Fprintln(a.out, "\nFormatting, Validating & Generating Prisma Client...")
			if err != nil {
				return err
			}
			for _, args := range [][]string{{"format"}, {"validate"}, {"generate"}} {
				if err := a.runPrisma(info, args...); err != nil {
					a.report(err)
					break
				}
			}
			time.Sleep(3 * time.Second)
		case "0":
			_, err := fmt.Fprintln(a.out, "\nExiting...")
			if err != nil {
				return err
			}
			time.Sleep(time.Second)
			return nil
		case "":
		default:
			_, err := fmt.Fprintln(a.out, "\nInvalid option.")
			if err != nil {
				return err
			}
			time.Sleep(1500 * time.Millisecond)
		}
	}
}

func (a *Console) migrationMenu(info prismaInfo) error {
	for {
		shared.Clear()
		_, err := fmt.Fprintf(a.out, "%s\n    Prisma Migration Console\n%s\n  1. Migrate\n  2. Reset\n  3. Status\n  4. Resolve\n  0. Back\n%s\n",
			separator, separator, separator)
		if err != nil {
			return err
		}
		if info.version == "" {
			_, err := fmt.Fprintln(a.out, info.problem)
			if err != nil {
				return err
			}
		}
		choice, err := a.prompt("> Select: ")
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch choice {
		case "1":
			_, err := fmt.Fprintln(a.out, "\nRunning Migrate...")
			if err != nil {
				return err
			}
			a.report(a.runPrisma(info, "migrate", "dev"))
			time.Sleep(3 * time.Second)
		case "2":
			_, err := fmt.Fprintln(a.out, "\nRunning Reset...")
			if err != nil {
				return err
			}
			cancelled, runErr := a.resetDatabase(info)
			if runErr != nil {
				a.report(runErr)
			}
			if !cancelled {
				time.Sleep(3 * time.Second)
			}
		case "3":
			_, err := fmt.Fprintln(a.out, "\nChecking Migration Status...")
			if err != nil {
				return err
			}
			a.report(a.runPrisma(info, "migrate", "status"))
			time.Sleep(3 * time.Second)
		case "4":
			cancelled, runErr := a.resolveMigration(info)
			if runErr != nil {
				a.report(runErr)
			}
			if !cancelled {
				time.Sleep(3 * time.Second)
			}
		case "0":
			return nil
		case "":
		default:
			_, err := fmt.Fprintln(a.out, "\nInvalid option.")
			if err != nil {
				return err
			}
			time.Sleep(1500 * time.Millisecond)
		}
	}
}

func (a *Console) Command(args []string) error {
	if err := a.validatePrismaProject(); err != nil {
		return err
	}
	info := a.detectPrisma()
	return a.runPrisma(info, args...)
}
