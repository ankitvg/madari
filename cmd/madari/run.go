package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/ankitvg/madari/internal/clients/claude"
	"github.com/ankitvg/madari/internal/doctor"
	"github.com/ankitvg/madari/internal/registry"
)

const version = "0.0.0-dev"

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printHelp(stdout)
		return 0
	}

	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, version)
		return 0
	case "--help", "-h":
		printHelp(stdout)
		return 0
	case "help":
		if len(args) == 1 {
			printHelp(stdout)
			return 0
		}
		if len(args) == 2 {
			if !printCommandHelp(args[1], stdout) {
				fmt.Fprintf(stderr, "error: unknown command: %s\n", args[1])
				return 1
			}
			return 0
		}
		fmt.Fprintln(stderr, "error: usage: madari help [command]")
		return 1
	}

	store, err := registry.NewDefaultStore()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return runWithStore(args, store, stdout, stderr)
}

func runWithStore(args []string, store *registry.Store, stdout, stderr io.Writer) int {
	app := cliApp{store: store, stdout: stdout, stderr: stderr}
	if err := app.dispatch(args); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

type cliApp struct {
	store  *registry.Store
	stdout io.Writer
	stderr io.Writer
}

func (a cliApp) dispatch(args []string) error {
	if len(args) == 0 {
		printHelp(a.stdout)
		return nil
	}

	switch args[0] {
	case "add":
		return a.cmdAdd(args[1:])
	case "list":
		return a.cmdList(args[1:])
	case "remove":
		return a.cmdRemove(args[1:])
	case "enable":
		return a.cmdSetEnabled(args[1:], true)
	case "disable":
		return a.cmdSetEnabled(args[1:], false)
	case "doctor":
		return a.cmdDoctor(args[1:])
	case "status":
		return a.cmdStatus(args[1:])
	case "sync":
		return a.cmdSync(args[1:])
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func (a cliApp) cmdAdd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: madari add <name> --command <cmd> --client <client>")
	}
	if isHelpToken(args[0]) {
		printAddHelp(a.stdout)
		return nil
	}
	name := args[0]

	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var command string
	var description string
	var disabled bool
	var cmdArgs stringList
	var clients stringList
	var envPairs stringList
	var requiredEnv stringList

	fs.StringVar(&command, "command", "", "Server command")
	fs.StringVar(&description, "description", "", "Server description")
	fs.BoolVar(&disabled, "disabled", false, "Create server in disabled state")
	fs.Var(&cmdArgs, "arg", "Command argument (repeatable)")
	fs.Var(&clients, "client", "Client id (repeatable)")
	fs.Var(&envPairs, "env", "Environment variable KEY=VALUE (repeatable)")
	fs.Var(&requiredEnv, "required-env", "Required environment key (repeatable)")

	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printAddHelp(a.stdout)
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("--command is required")
	}
	if len(clients) == 0 {
		return fmt.Errorf("at least one --client is required")
	}

	env, err := parseEnvPairs(envPairs)
	if err != nil {
		return err
	}
	resolvedCommand, err := resolveCommandPath(command)
	if err != nil {
		return err
	}

	manifest := registry.Manifest{
		Name:        name,
		Command:     resolvedCommand,
		Args:        append([]string(nil), cmdArgs...),
		Enabled:     !disabled,
		Clients:     append([]string(nil), clients...),
		Description: description,
		Env:         env,
		RequiredEnv: registry.RequiredEnv{Keys: append([]string(nil), requiredEnv...)},
	}

	if err := a.store.Add(manifest); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "added %s\n", name)
	return nil
}

func (a cliApp) cmdList(args []string) error {
	if len(args) == 1 && isHelpToken(args[0]) {
		printListHelp(a.stdout)
		return nil
	}
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printListHelp(a.stdout)
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: madari list")
	}

	manifests, err := a.store.List()
	if err != nil {
		return err
	}
	if len(manifests) == 0 {
		fmt.Fprintln(a.stdout, "no servers configured")
		return nil
	}

	fmt.Fprintln(a.stdout, "NAME\tSTATUS\tCOMMAND\tCLIENTS")
	for _, manifest := range manifests {
		status := "disabled"
		if manifest.Enabled {
			status = "enabled"
		}
		clients := append([]string(nil), manifest.Clients...)
		sort.Strings(clients)
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\n", manifest.Name, status, manifest.Command, strings.Join(clients, ","))
	}
	return nil
}

func (a cliApp) cmdRemove(args []string) error {
	if len(args) == 1 && isHelpToken(args[0]) {
		printRemoveHelp(a.stdout)
		return nil
	}
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printRemoveHelp(a.stdout)
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: madari remove <name>")
	}
	name := fs.Arg(0)

	if err := a.store.Remove(name); err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			return fmt.Errorf("server %q not found", name)
		}
		return err
	}
	fmt.Fprintf(a.stdout, "removed %s\n", name)
	return nil
}

func (a cliApp) cmdSetEnabled(args []string, enabled bool) error {
	command := "enable"
	if !enabled {
		command = "disable"
	}
	if len(args) == 1 && isHelpToken(args[0]) {
		printEnableDisableHelp(command, a.stdout)
		return nil
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printEnableDisableHelp(command, a.stdout)
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: madari %s <name>", command)
	}
	name := fs.Arg(0)

	if err := a.store.SetEnabled(name, enabled); err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			return fmt.Errorf("server %q not found", name)
		}
		return err
	}

	state := "enabled"
	if !enabled {
		state = "disabled"
	}
	fmt.Fprintf(a.stdout, "%s %s\n", name, state)
	return nil
}

func (a cliApp) cmdSync(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: madari sync <client> [--dry-run] [--config-path <path>]")
	}
	if isHelpToken(args[0]) {
		printSyncHelp(a.stdout)
		return nil
	}
	target := strings.TrimSpace(args[0])

	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var dryRun bool
	var configPath string
	fs.BoolVar(&dryRun, "dry-run", false, "Preview changes without writing files")
	fs.StringVar(&configPath, "config-path", "", "Override client config path")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printSyncHelp(a.stdout)
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if target != claude.Target {
		return fmt.Errorf("unsupported sync target %q (supported: %s)", target, claude.Target)
	}

	manifests, err := a.store.List()
	if err != nil {
		return err
	}
	syncable, skipped := filterSyncableClaudeManifests(manifests)

	statePath := filepath.Join(filepath.Dir(a.store.ServersDir()), "state", target+"-managed.json")
	result, err := claude.Sync(syncable, claude.SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
		DryRun:     dryRun,
	})
	if err != nil {
		return err
	}

	mode := "applied"
	if result.DryRun {
		mode = "dry-run"
	}

	fmt.Fprintf(a.stdout, "sync target: %s\n", target)
	fmt.Fprintf(a.stdout, "config path: %s\n", result.ConfigPath)
	fmt.Fprintf(a.stdout, "mode: %s\n", mode)
	fmt.Fprintf(a.stdout, "added: %s\n", formatNameList(result.Added))
	fmt.Fprintf(a.stdout, "updated: %s\n", formatNameList(result.Updated))
	fmt.Fprintf(a.stdout, "removed: %s\n", formatNameList(result.Removed))
	if len(skipped) > 0 {
		fmt.Fprintf(a.stdout, "skipped: %s\n", formatNameList(skipped))
		for _, name := range skipped {
			fmt.Fprintf(a.stderr, "warning: skipped %s because command path is not an executable file\n", name)
		}
	}
	if len(result.Unchanged) > 0 {
		fmt.Fprintf(a.stdout, "unchanged: %s\n", formatNameList(result.Unchanged))
	}
	if !result.HasChanges() {
		fmt.Fprintln(a.stdout, "no changes")
	}
	return nil
}

func (a cliApp) cmdDoctor(args []string) error {
	if len(args) == 1 && isHelpToken(args[0]) {
		printDoctorHelp(a.stdout)
		return nil
	}

	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var configPath string
	fs.StringVar(&configPath, "config-path", "", "Override Claude Desktop config path")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printDoctorHelp(a.stdout)
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}

	report, err := doctor.Run(a.store, doctor.Options{
		ClaudeConfigPath: configPath,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "servers directory: %s\n", report.ServersDir)
	fmt.Fprintf(a.stdout, "claude config: %s [%s]\n", report.ClaudeConfig.Path, report.ClaudeConfig.Status)
	if report.ClaudeConfig.Message != "" {
		fmt.Fprintf(a.stdout, "claude detail: %s\n", report.ClaudeConfig.Message)
	}

	if len(report.ManifestErrors) > 0 {
		fmt.Fprintln(a.stdout, "manifest errors:")
		for _, manifestError := range report.ManifestErrors {
			fmt.Fprintf(a.stdout, "  - %s: %s\n", manifestError.File, manifestError.Message)
		}
	}

	if len(report.Servers) == 0 {
		fmt.Fprintln(a.stdout, "servers: none")
	} else {
		fmt.Fprintln(a.stdout, "servers:")
		for _, server := range report.Servers {
			fmt.Fprintf(
				a.stdout,
				"  - %s [%s] enabled=%t command=%s clients=%s\n",
				server.Name,
				server.Status,
				server.Enabled,
				server.Command,
				strings.Join(server.Clients, ","),
			)
			for _, issue := range server.Issues {
				fmt.Fprintf(a.stdout, "      * [%s] %s\n", issue.Severity, issue.Message)
			}
		}
	}

	fmt.Fprintf(
		a.stdout,
		"summary: total=%d ready=%d warn=%d error=%d skipped=%d\n",
		report.Summary.Total,
		report.Summary.Ready,
		report.Summary.Warning,
		report.Summary.Error,
		report.Summary.Skipped,
	)

	if report.Summary.Error > 0 {
		return fmt.Errorf("doctor found %d error(s)", report.Summary.Error)
	}
	return nil
}

func (a cliApp) cmdStatus(args []string) error {
	if len(args) == 1 && isHelpToken(args[0]) {
		printStatusHelp(a.stdout)
		return nil
	}

	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var configPath string
	fs.StringVar(&configPath, "config-path", "", "Override Claude Desktop config path")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printStatusHelp(a.stdout)
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}

	report, err := doctor.Run(a.store, doctor.Options{
		ClaudeConfigPath: configPath,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(
		a.stdout,
		"madari: total=%d ready=%d warn=%d error=%d skipped=%d\n",
		report.Summary.Total,
		report.Summary.Ready,
		report.Summary.Warning,
		report.Summary.Error,
		report.Summary.Skipped,
	)
	fmt.Fprintf(a.stdout, "claude-config: %s\n", report.ClaudeConfig.Status)
	if len(report.ManifestErrors) > 0 {
		fmt.Fprintf(a.stdout, "manifest-errors: %d\n", len(report.ManifestErrors))
	}
	fmt.Fprintln(a.stdout, "hint: run `madari doctor` for detailed diagnostics")

	if report.Summary.Error > 0 {
		return fmt.Errorf("status found %d error(s)", report.Summary.Error)
	}
	return nil
}

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func parseEnvPairs(pairs []string) (map[string]string, error) {
	env := map[string]string{}
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("invalid env assignment %q, expected KEY=VALUE", pair)
		}
		if _, exists := env[key]; exists {
			return nil, fmt.Errorf("duplicate env key %q", key)
		}
		env[key] = value
	}
	return env, nil
}

func filterSyncableClaudeManifests(manifests []registry.Manifest) ([]registry.Manifest, []string) {
	out := make([]registry.Manifest, 0, len(manifests))
	var skipped []string
	for _, manifest := range manifests {
		if !manifest.Enabled || !hasClaudeTarget(manifest.Clients) {
			out = append(out, manifest)
			continue
		}
		if err := validateAbsoluteExecutablePath(manifest.Command); err != nil {
			skipped = append(skipped, manifest.Name)
			continue
		}
		out = append(out, manifest)
	}
	sort.Strings(skipped)
	return out, skipped
}

func hasClaudeTarget(clients []string) bool {
	for _, client := range clients {
		if strings.EqualFold(strings.TrimSpace(client), claude.Target) {
			return true
		}
	}
	return false
}

func resolveCommandPath(command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("--command is required")
	}

	if filepath.IsAbs(command) || strings.ContainsRune(command, filepath.Separator) {
		path := command
		if !filepath.IsAbs(path) {
			absPath, err := filepath.Abs(path)
			if err != nil {
				return "", fmt.Errorf("resolve command %q: %w", command, err)
			}
			path = absPath
		}
		cleaned := filepath.Clean(path)
		if err := validateAbsoluteExecutablePath(cleaned); err != nil {
			return "", fmt.Errorf("resolve command %q: %w", command, err)
		}
		return cleaned, nil
	}

	lookedUp, err := exec.LookPath(command)
	if err != nil {
		return "", fmt.Errorf("resolve command %q: not found in PATH", command)
	}
	absPath, err := filepath.Abs(lookedUp)
	if err != nil {
		return "", fmt.Errorf("resolve command %q: %w", command, err)
	}
	cleaned := filepath.Clean(absPath)
	if err := validateAbsoluteExecutablePath(cleaned); err != nil {
		return "", fmt.Errorf("resolve command %q: %w", command, err)
	}
	return cleaned, nil
}

func validateAbsoluteExecutablePath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path must be absolute")
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("path does not exist: %s", path)
		}
		return fmt.Errorf("stat path %q: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("path is a directory: %s", path)
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return fmt.Errorf("path is not executable: %s", path)
	}
	return nil
}

func formatNameList(names []string) string {
	if len(names) == 0 {
		return "-"
	}
	return strings.Join(names, ",")
}

func isHelpToken(value string) bool {
	return value == "--help" || value == "-h"
}

func printCommandHelp(command string, out io.Writer) bool {
	switch strings.TrimSpace(command) {
	case "add":
		printAddHelp(out)
	case "list":
		printListHelp(out)
	case "remove":
		printRemoveHelp(out)
	case "enable":
		printEnableDisableHelp("enable", out)
	case "disable":
		printEnableDisableHelp("disable", out)
	case "sync":
		printSyncHelp(out)
	case "doctor":
		printDoctorHelp(out)
	case "status":
		printStatusHelp(out)
	default:
		return false
	}
	return true
}

func printAddHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari add <name> --command <cmd> --client <client> [options]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --command <cmd>            Server command (required)")
	fmt.Fprintln(out, "  --client <client>          Target client id (required, repeatable)")
	fmt.Fprintln(out, "  --arg <value>              Command argument (repeatable)")
	fmt.Fprintln(out, "  --env KEY=VALUE            Environment variable (repeatable)")
	fmt.Fprintln(out, "  --required-env <KEY>       Required runtime env key (repeatable)")
	fmt.Fprintln(out, "  --description <text>       Server description")
	fmt.Fprintln(out, "  --disabled                 Add server in disabled state")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Examples:")
	fmt.Fprintln(out, "  madari add stewreads --command stewreads-mcp --client claude-desktop")
	fmt.Fprintln(out, "  madari add mailer --command ./bin/mailer --client claude-desktop --required-env SMTP_PASSWORD")
}

func printListHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari list")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  List configured servers with status, command path, and clients.")
}

func printRemoveHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari remove <name>")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Remove a server from Madari registry.")
}

func printEnableDisableHelp(command string, out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintf(out, "  madari %s <name>\n", command)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	if command == "enable" {
		fmt.Fprintln(out, "  Enable a server so sync can include it for target clients.")
		return
	}
	fmt.Fprintln(out, "  Disable a server so sync excludes it for target clients.")
}

func printSyncHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari sync claude-desktop [--dry-run] [--config-path <path>]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --dry-run                  Preview changes without writing files")
	fmt.Fprintln(out, "  --config-path <path>       Override Claude config path")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Sync enabled claude-desktop servers from Madari registry into Claude config.")
}

func printDoctorHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari doctor [--config-path <path>]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --config-path <path>       Override Claude config path for diagnostics")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Validate server manifests, command paths, required env keys, and Claude config health.")
}

func printStatusHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari status [--config-path <path>]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --config-path <path>       Override Claude config path for status checks")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Show a concise readiness summary. Use `madari doctor` for full details.")
}

func printHelp(out io.Writer) {
	fmt.Fprintln(out, "madari - local MCP manager")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  add       Add a server manifest")
	fmt.Fprintln(out, "  list      List configured servers")
	fmt.Fprintln(out, "  remove    Remove a server manifest")
	fmt.Fprintln(out, "  enable    Enable a server")
	fmt.Fprintln(out, "  disable   Disable a server")
	fmt.Fprintln(out, "  sync      Sync server manifests to a client config")
	fmt.Fprintln(out, "  doctor    Run diagnostics on local MCP setup")
	fmt.Fprintln(out, "  status    Show concise readiness summary")
	fmt.Fprintln(out, "  help      Show help")
	fmt.Fprintln(out, "  version   Show version")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Run `madari help <command>` for command-specific help.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Examples:")
	fmt.Fprintln(out, "  madari add stewreads --command stewreads-mcp --client claude-desktop")
	fmt.Fprintln(out, "  madari list")
	fmt.Fprintln(out, "  madari disable stewreads")
	fmt.Fprintln(out, "  madari sync claude-desktop --dry-run")
	fmt.Fprintln(out)
	defaultRoot, rootErr := registry.DefaultRootDir()
	defaultServers, serversErr := registry.DefaultServersDir()
	if rootErr == nil {
		fmt.Fprintf(out, "Default config directory: %s\n", defaultRoot)
	}
	if serversErr == nil {
		fmt.Fprintf(out, "Default servers directory: %s\n", defaultServers)
	}
	fmt.Fprintf(out, "Override config directory with: %s\n", registry.ConfigDirEnvVar)
}
