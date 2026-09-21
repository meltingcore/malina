package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/meltingcore/malina/internal/core"
	"golang.org/x/term"
)

const usage = `malina — live whole-device backup for Raspberry Pi

Usage:
  malina inspect --host user@host [--identity PATH] [--password] [--port 22]
  malina backup --host user@host --output DIRECTORY [--identity PATH] [--password]
  malina backups --output DIRECTORY
  malina verify BACKUP_DIRECTORY
  malina devices
  malina restore BACKUP_DIRECTORY --device DEVICE --confirm DEVICE [--no-verify]
`

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func connectionFlags(set *flag.FlagSet) (*string, *string, *int, *bool) {
	host := set.String("host", "", "Raspberry Pi SSH address")
	identity := set.String("identity", "", "SSH private key path")
	port := set.Int("port", 0, "SSH port")
	password := set.Bool("password", false, "Prompt for the SSH account password (also used for sudo)")
	return host, identity, port, password
}

func connection(host, identity string, port int, promptForPassword bool) (core.Connection, error) {
	if identity != "" {
		identity, _ = filepath.Abs(identity)
	}
	password := ""
	if promptForPassword {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return core.Connection{}, core.NewError("PASSWORD_PROMPT_FAILED", "SSH password prompting requires an interactive terminal.")
		}
		fmt.Fprint(os.Stderr, "SSH account password: ")
		value, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return core.Connection{}, core.WrapError("PASSWORD_PROMPT_FAILED", "Cannot read the SSH password.", err)
		}
		password = string(value)
	}
	return core.Connection{Host: host, Identity: identity, Password: password, Port: port}, nil
}

func progress(event core.Progress) {
	info, _ := os.Stderr.Stat()
	if info == nil || info.Mode()&os.ModeCharDevice == 0 {
		return
	}
	percentage := ""
	if event.TotalBytes > 0 || event.Fraction > 0 {
		percentage = fmt.Sprintf(" %.1f%%", event.Fraction*100)
	}
	fmt.Fprintf(os.Stderr, "\r\033[2K%s%s", event.Message, percentage)
	if event.Phase == "complete" {
		fmt.Fprintln(os.Stderr)
	}
}

func execute(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Print(usage)
		return nil
	}
	ctx := context.Background()
	engine := core.NewEngine()
	switch args[0] {
	case "inspect":
		set := flag.NewFlagSet("inspect", flag.ContinueOnError)
		host, identity, port, password := connectionFlags(set)
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		conn, err := connection(*host, *identity, *port, *password)
		if err != nil {
			return err
		}
		result, err := engine.Inspect(ctx, conn)
		if err != nil {
			return err
		}
		return printJSON(result)
	case "backup":
		set := flag.NewFlagSet("backup", flag.ContinueOnError)
		host, identity, port, password := connectionFlags(set)
		output := set.String("output", "malina-backups", "Backup directory")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		conn, err := connection(*host, *identity, *port, *password)
		if err != nil {
			return err
		}
		result, err := engine.Backup(ctx, core.BackupRequest{
			Connection:      conn,
			OutputDirectory: *output,
		}, progress)
		if err != nil {
			return err
		}
		return printJSON(result)
	case "backups":
		set := flag.NewFlagSet("backups", flag.ContinueOnError)
		output := set.String("output", "malina-backups", "Backup directory")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		result, err := core.ListBackups(*output)
		if err != nil {
			return err
		}
		return printJSON(result)
	case "verify":
		set := flag.NewFlagSet("verify", flag.ContinueOnError)
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if set.NArg() != 1 {
			return core.NewError("BACKUP_REQUIRED", "Pass the backup directory to verify.")
		}
		result, err := engine.Verify(ctx, set.Arg(0), progress)
		if err != nil {
			return err
		}
		return printJSON(result)
	case "devices":
		result, err := engine.Devices.List(ctx)
		if err != nil {
			return err
		}
		return printJSON(result)
	case "restore":
		set := flag.NewFlagSet("restore", flag.ContinueOnError)
		device := set.String("device", "", "Destination device")
		confirm := set.String("confirm", "", "Exact destination device confirmation")
		noVerify := set.Bool("no-verify", false, "Skip read-back verification")
		restoreArgs := args[1:]
		if len(restoreArgs) > 0 && !strings.HasPrefix(restoreArgs[0], "-") {
			restoreArgs = append(append([]string{}, restoreArgs[1:]...), restoreArgs[0])
		}
		if err := set.Parse(restoreArgs); err != nil {
			return err
		}
		if set.NArg() != 1 {
			return core.NewError("BACKUP_REQUIRED", "Pass the backup directory to restore.")
		}
		result, err := engine.Restore(ctx, core.RestoreRequest{
			BackupPath: set.Arg(0), Device: *device, Confirm: *confirm, Verify: !*noVerify,
		}, progress)
		if err != nil {
			return err
		}
		return printJSON(result)
	default:
		return core.NewError("UNKNOWN_COMMAND", "Unknown command: "+args[0]+"\n\n"+usage)
	}
}

func main() {
	if err := execute(os.Args[1:]); err != nil {
		_ = printJSON(core.ErrorPayload(err))
		os.Exit(1)
	}
}
