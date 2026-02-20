package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

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
	case "help", "--help", "-h":
		printHelp(stdout)
		return 0
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
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func (a cliApp) cmdAdd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: madari add <name> --command <cmd> --client <client>")
	}
	name := args[0]

	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(a.stderr)

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

	manifest := registry.Manifest{
		Name:        name,
		Command:     command,
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
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
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
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
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
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
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

func printHelp(out io.Writer) {
	fmt.Fprintln(out, "madari - local MCP manager")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  add       Add a server manifest")
	fmt.Fprintln(out, "  list      List configured servers")
	fmt.Fprintln(out, "  remove    Remove a server manifest")
	fmt.Fprintln(out, "  enable    Enable a server")
	fmt.Fprintln(out, "  disable   Disable a server")
	fmt.Fprintln(out, "  help      Show help")
	fmt.Fprintln(out, "  version   Show version")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Examples:")
	fmt.Fprintln(out, "  madari add stewreads --command stewreads-mcp --client claude-desktop")
	fmt.Fprintln(out, "  madari list")
	fmt.Fprintln(out, "  madari disable stewreads")
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
