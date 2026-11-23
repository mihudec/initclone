// initclone populates mounted volumes from a YAML manifest using embedded rclone.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"filippo.io/age"

	_ "github.com/rclone/rclone/backend/all"
	"github.com/rclone/rclone/cmd"
	_ "github.com/rclone/rclone/cmd/copyto"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	// Run transfers in a child copy of this executable: rclone owns process-global
	// flags and may call os.Exit. Isolation also resets its state for each entry.
	if len(os.Args) > 1 && os.Args[1] == "__rclone" {
		os.Args = append([]string{"rclone"}, os.Args[2:]...)
		cmd.Main()
		return
	}
	if len(os.Args) > 1 {
		if len(os.Args) == 2 && os.Args[1] == "--version" {
			fmt.Printf("initclone %s (commit %s, built %s)\n", version, commit, date)
			return
		}
		if len(os.Args) == 2 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
			fmt.Println("Usage: initclone [--help | --version]\n\nConfigure via CONFIG_DIR, FILES_CONFIG, RCLONE_CONFIG, DEST_ROOT,\nRCLONE_ARGS and LOG_LEVEL. No separate rclone installation is required.")
			return
		}
		fmt.Fprintln(os.Stderr, "ERROR unexpected arguments; use --help")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Getenv, os.Stderr, copyRemote); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR", err)
		os.Exit(1)
	}
}

type copier func(context.Context, string, string, string, []string) error

func copyRemote(ctx context.Context, remote, destination, config string, extra []string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	args := append([]string{"__rclone", "copyto", remote, destination, "--config", config}, extra...)
	child := exec.CommandContext(ctx, executable, args...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := child.Run(); err != nil {
		return fmt.Errorf("copy %s: %w", remote, err)
	}
	return nil
}

func run(ctx context.Context, getenv func(string) string, output io.Writer, copy copier) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("Unix ownership and permissions require Linux or macOS")
	}
	env := func(key, fallback string) string {
		if value := getenv(key); value != "" {
			return value
		}
		return fallback
	}
	level := env("LOG_LEVEL", "INFO")
	switch level {
	case "DEBUG", "INFO", "WARNING", "WARN", "ERROR", "CRITICAL", "FATAL", "NOTSET":
	default:
		return fmt.Errorf("invalid LOG_LEVEL %q", level)
	}
	logger := log.New(output, "INFO ", 0)
	info := func(format string, args ...any) {
		if level == "DEBUG" || level == "INFO" || level == "NOTSET" {
			logger.Printf(format, args...)
		}
	}
	configDir := env("CONFIG_DIR", "/config")
	configPath := env("RCLONE_CONFIG", filepath.Join(configDir, "rclone.conf"))
	stat, err := os.Stat(configPath)
	if err != nil {
		return fmt.Errorf("rclone config: %w", err)
	}
	if !stat.Mode().IsRegular() {
		return fmt.Errorf("rclone config is not a regular file: %s", configPath)
	}
	root := env("DEST_ROOT", "/")
	entries, err := loadManifest(env("FILES_CONFIG", filepath.Join(configDir, "files.yaml")), root)
	if err != nil {
		return err
	}
	// Load all identities before transferring anything, including read-only Secret mounts.
	identities := make([][]age.Identity, len(entries))
	for i, entry := range entries {
		if entry.IdentityFile != "" {
			identities[i], err = loadIdentities(entry.IdentityFile)
			if err != nil {
				return err
			}
		}
	}
	extra, err := splitArgs(getenv("RCLONE_ARGS"))
	if err != nil {
		return err
	}
	temporary, err := os.MkdirTemp("", "initclone-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	workingConfig := filepath.Join(temporary, "rclone.conf")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(workingConfig, data, 0600); err != nil {
		return err
	}
	for i, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Recheck before each transfer in case a preceding copy changed a symlink.
		destination, err := resolveDestination(entry.Destination, root)
		if err != nil {
			return err
		}
		if destination != entry.Path {
			return fmt.Errorf("destination changed after validation: %s", entry.Destination)
		}
		if err := os.MkdirAll(filepath.Dir(entry.Path), 0755); err != nil {
			return err
		}
		info("copying %s to %s (%d/%d)", entry.Remote, entry.Path, i+1, len(entries))
		if err := copy(ctx, entry.Remote, entry.Path, workingConfig, extra); err != nil {
			return err
		}
		if len(identities[i]) != 0 {
			info("decrypting .age files in %s", entry.Path)
			if err := decryptFiles(ctx, entry, identities[i]); err != nil {
				return err
			}
		}
		if err := applyMetadata(entry.Path, entry.Mode, entry.UID, entry.GID); err != nil {
			return err
		}
	}
	info("populated %d destination(s)", len(entries))
	return nil
}
