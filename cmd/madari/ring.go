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

func (a cliApp) cmdRing(args []string) error {
	if len(args) == 0 {
		return commandUsageError("ring", "madari ring <create|list|show> [options]")
	}
	if isHelpToken(args[0]) {
		printRingHelp(a.stdout)
		return nil
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "create":
		return a.cmdRingCreate(rest)
	case "list":
		return a.cmdRingList(rest)
	case "show":
		return a.cmdRingShow(rest)
	default:
		return commandInputError("ring", fmt.Sprintf("unknown ring subcommand %q (supported: create, list, show)", sub))
	}
}

func (a cliApp) cmdRingCreate(args []string) error {
	if len(args) == 0 {
		return commandUsageError("ring create", "madari ring create <name> --member <server> [--member ...] [--description <text>]")
	}
	if isHelpToken(args[0]) {
		printRingCreateHelp(a.stdout)
		return nil
	}
	name := strings.TrimSpace(args[0])

	fs := flag.NewFlagSet("ring create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var members stringList
	var description string
	fs.Var(&members, "member", "Ring member server name (repeatable)")
	fs.StringVar(&description, "description", "", "Ring description")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printRingCreateHelp(a.stdout)
			return nil
		}
		return commandInputError("ring create", err.Error())
	}
	if fs.NArg() != 0 {
		return commandUnexpectedArgsError("ring create", fs.Args())
	}

	ring := registry.Ring{
		Name:        name,
		Members:     append([]string(nil), members...),
		Description: description,
	}
	if err := a.store.AddRing(ring); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "created ring %s with %d member(s)\n", name, len(members))
	return nil
}

func (a cliApp) cmdRingList(args []string) error {
	if len(args) == 1 && isHelpToken(args[0]) {
		printRingListHelp(a.stdout)
		return nil
	}
	fs := flag.NewFlagSet("ring list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Emit JSON instead of text")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printRingListHelp(a.stdout)
			return nil
		}
		return commandInputError("ring list", err.Error())
	}
	if fs.NArg() != 0 {
		return commandUnexpectedArgsError("ring list", fs.Args())
	}

	rings, err := a.store.ListRings()
	if err != nil {
		return err
	}

	if jsonOut {
		payload := ringListJSON{
			SchemaVersion: jsonSchemaVersion,
			Command:       "ring list",
			Rings:         make([]ringJSON, 0, len(rings)),
		}
		for _, ring := range rings {
			payload.Rings = append(payload.Rings, ringToJSON(ring))
		}
		return writeJSON(a.stdout, payload)
	}

	if len(rings) == 0 {
		fmt.Fprintln(a.stdout, "no rings configured")
		return nil
	}
	fmt.Fprintln(a.stdout, "NAME\tMEMBERS\tDESCRIPTION")
	for _, ring := range rings {
		members := append([]string(nil), ring.Members...)
		sort.Strings(members)
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", ring.Name, strings.Join(members, ","), ring.Description)
	}
	return nil
}

func (a cliApp) cmdRingShow(args []string) error {
	if len(args) == 0 {
		return commandUsageError("ring show", "madari ring show <name> [--json]")
	}
	if isHelpToken(args[0]) {
		printRingShowHelp(a.stdout)
		return nil
	}
	name := strings.TrimSpace(args[0])

	fs := flag.NewFlagSet("ring show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Emit JSON instead of text")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printRingShowHelp(a.stdout)
			return nil
		}
		return commandInputError("ring show", err.Error())
	}
	if fs.NArg() != 0 {
		return commandUnexpectedArgsError("ring show", fs.Args())
	}

	ring, err := a.store.GetRing(name)
	if err != nil {
		if errors.Is(err, registry.ErrRingNotFound) {
			return fmt.Errorf("ring %q not found", name)
		}
		return err
	}

	if jsonOut {
		return writeJSON(a.stdout, ringShowJSON{
			SchemaVersion: jsonSchemaVersion,
			Command:       "ring show",
			Ring:          ringToJSON(ring),
		})
	}

	fmt.Fprintf(a.stdout, "name: %s\n", ring.Name)
	if strings.TrimSpace(ring.Description) != "" {
		fmt.Fprintf(a.stdout, "description: %s\n", ring.Description)
	}
	fmt.Fprintln(a.stdout, "members:")
	members := append([]string(nil), ring.Members...)
	sort.Strings(members)
	for _, member := range members {
		fmt.Fprintf(a.stdout, "  - %s\n", member)
	}
	return nil
}

func printRingHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari ring <subcommand> [options]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Subcommands:")
	fmt.Fprintln(out, "  create    Create a ring from registry servers")
	fmt.Fprintln(out, "  list      List configured rings")
	fmt.Fprintln(out, "  show      Show one ring's members")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Rings are named capability sets of MCP servers. Members reference")
	fmt.Fprintln(out, "  registry entries by name; the server manifest stays the single source")
	fmt.Fprintln(out, "  of truth for command, args, and env.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Run `madari ring <subcommand> --help` for subcommand help.")
}

func printRingCreateHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari ring create <name> --member <server> [--member ...] [--description <text>]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --member <server>          Ring member server name (repeatable, required)")
	fmt.Fprintln(out, "  --description <text>       Ring description")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Create a ring referencing existing registry servers by name. Every")
	fmt.Fprintln(out, "  member must already exist in the registry.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Examples:")
	fmt.Fprintln(out, "  madari ring create research --member stewreads --member arxiv")
}

func printRingListHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari ring list [--json]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --json                     Emit JSON instead of text")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  List configured rings with members and description.")
}

func printRingShowHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari ring show <name> [--json]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --json                     Emit JSON instead of text")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Show one ring's members and description.")
}
