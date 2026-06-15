package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/ankitvg/madari/internal/clients"
	"github.com/ankitvg/madari/internal/clients/claudecode"
	"github.com/ankitvg/madari/internal/clients/syncshared"
	"github.com/ankitvg/madari/internal/registry"
)

func (a cliApp) cmdRing(args []string) error {
	if len(args) == 0 {
		return commandUsageError("ring", "madari ring <create|list|show|attach|detach|delete|render|status> [options]")
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
	case "attach":
		return a.cmdRingAttach(rest)
	case "detach":
		return a.cmdRingDetach(rest)
	case "delete":
		return a.cmdRingDelete(rest)
	case "render":
		return a.cmdRingRender(rest)
	case "status":
		return a.cmdRingStatus(rest)
	default:
		return commandInputError("ring", fmt.Sprintf("unknown ring subcommand %q (supported: create, list, show, attach, detach, delete, render, status)", sub))
	}
}

// ringTargetStatus is the per-(target, scope) view ring status reports.
type ringTargetStatus struct {
	target  string
	scope   string
	rings   []ringAttachment
	servers []serverSources
}

type ringAttachment struct {
	name           string
	exists         bool
	members        []string
	owned          []string
	pending        []string
	stale          []string
	missingMembers []string
}

type serverSources struct {
	name    string
	sources []string
}

func (a cliApp) cmdRingStatus(args []string) error {
	if len(args) == 1 && isHelpToken(args[0]) {
		printRingStatusHelp(a.stdout)
		return nil
	}
	fs := flag.NewFlagSet("ring status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Emit JSON instead of text")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printRingStatusHelp(a.stdout)
			return nil
		}
		return commandInputError("ring status", err.Error())
	}
	if fs.NArg() != 0 {
		return commandUnexpectedArgsError("ring status", fs.Args())
	}

	manifests, err := a.store.List()
	if err != nil {
		return err
	}
	manifestNames := make(map[string]bool, len(manifests))
	for _, manifest := range manifests {
		manifestNames[manifest.Name] = true
	}

	statuses := []ringTargetStatus{}
	for _, ref := range a.managedStateRefs() {
		state, err := syncshared.LoadManagedState(ref.path)
		if err != nil {
			return err
		}
		ts := ringTargetStatus{target: ref.target, scope: ref.scope}

		for _, name := range syncshared.AttachedRings(state) {
			attachment := ringAttachment{name: name}
			ring, err := a.store.GetRing(name)
			switch {
			case errors.Is(err, registry.ErrRingNotFound):
				// exists stays false; stale sources released via detach.
			case err != nil:
				return err
			default:
				attachment.exists = true
				members := append([]string(nil), ring.Members...)
				sort.Strings(members)
				attachment.members = members
				source := syncshared.RingSource(name)
				memberSet := make(map[string]bool, len(members))
				for _, member := range members {
					member = strings.TrimSpace(member)
					memberSet[member] = true
					if slices.Contains(state[member], source) {
						attachment.owned = append(attachment.owned, member)
					} else {
						attachment.pending = append(attachment.pending, member)
					}
					if !manifestNames[member] {
						attachment.missingMembers = append(attachment.missingMembers, member)
					}
				}
				// Stale owners: state entries still carrying the source
				// after a membership edit removed them from the ring.
				for owner, sources := range state {
					if !memberSet[owner] && slices.Contains(sources, source) {
						attachment.stale = append(attachment.stale, owner)
					}
				}
				sort.Strings(attachment.stale)
			}
			ts.rings = append(ts.rings, attachment)
		}

		serverNames := syncshared.MapKeys(state)
		sort.Strings(serverNames)
		for _, name := range serverNames {
			ts.servers = append(ts.servers, serverSources{name: name, sources: state[name]})
		}
		statuses = append(statuses, ts)
	}

	if jsonOut {
		payload := ringStatusJSON{
			SchemaVersion: jsonSchemaVersion,
			Command:       "ring status",
			Targets:       make([]ringStatusTargetJSON, 0, len(statuses)),
		}
		for _, ts := range statuses {
			scope := ts.scope
			if scope == "" {
				scope = "default"
			}
			targetJSON := ringStatusTargetJSON{
				Target:  ts.target,
				Scope:   scope,
				Rings:   make([]ringAttachmentJSON, 0, len(ts.rings)),
				Servers: make([]ringServerJSON, 0, len(ts.servers)),
			}
			for _, att := range ts.rings {
				targetJSON.Rings = append(targetJSON.Rings, ringAttachmentJSON{
					Name:           att.name,
					Exists:         att.exists,
					Members:        nonNilStrings(att.members),
					Owned:          nonNilStrings(att.owned),
					Pending:        nonNilStrings(att.pending),
					Stale:          nonNilStrings(att.stale),
					MissingMembers: nonNilStrings(att.missingMembers),
				})
			}
			for _, server := range ts.servers {
				targetJSON.Servers = append(targetJSON.Servers, ringServerJSON{
					Name:    server.name,
					Sources: nonNilStrings(server.sources),
				})
			}
			payload.Targets = append(payload.Targets, targetJSON)
		}
		return writeJSON(a.stdout, payload)
	}

	for _, ts := range statuses {
		label := ts.target
		if ts.scope == clients.ScopeUser {
			label += " (user scope)"
		}
		if len(ts.servers) == 0 {
			fmt.Fprintf(a.stdout, "%s: no managed entries\n", label)
			continue
		}
		syncHint := "madari sync " + ts.target
		if ts.scope == clients.ScopeUser {
			syncHint += " --scope user"
		}
		fmt.Fprintf(a.stdout, "%s:\n", label)
		if len(ts.rings) == 0 {
			fmt.Fprintln(a.stdout, "  rings: -")
		} else {
			fmt.Fprintln(a.stdout, "  rings:")
			for _, att := range ts.rings {
				if !att.exists {
					fix := fmt.Sprintf("madari ring detach %s %s", att.name, ts.target)
					if ts.scope == clients.ScopeUser {
						fix += " --scope user"
					}
					fmt.Fprintf(a.stdout, "    %s [missing] ring file not found; release with `%s` (pass --config-path if it was attached to a custom config)\n", att.name, fix)
					continue
				}
				marker := "ok"
				if len(att.pending)+len(att.stale) > 0 {
					marker = "out-of-sync"
				}
				line := fmt.Sprintf("    %s [%s] members=%d owned=%d", att.name, marker, len(att.members), len(att.owned))
				if len(att.pending) > 0 {
					line += fmt.Sprintf(" pending=%s", strings.Join(att.pending, ","))
				}
				if len(att.stale) > 0 {
					line += fmt.Sprintf(" stale=%s", strings.Join(att.stale, ","))
				}
				if len(att.pending)+len(att.stale) > 0 {
					line += fmt.Sprintf(" (run `%s`; pass --config-path if attached to a custom config)", syncHint)
				}
				if len(att.missingMembers) > 0 {
					line += fmt.Sprintf(" missing-from-registry=%s", strings.Join(att.missingMembers, ","))
				}
				fmt.Fprintln(a.stdout, line)
			}
		}
		fmt.Fprintln(a.stdout, "  servers:")
		for _, server := range ts.servers {
			fmt.Fprintf(a.stdout, "    %s: %s\n", server.name, strings.Join(server.sources, ", "))
		}
	}
	return nil
}

func printRingStatusHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari ring status [--json]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --json                     Emit JSON instead of text")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Show attached rings and per-server ownership sources for every client")
	fmt.Fprintln(out, "  and scope. Flags rings whose file is missing (release with ring detach)")
	fmt.Fprintln(out, "  and members that are pending sync or missing from the registry.")
}

func (a cliApp) cmdRingRender(args []string) error {
	if len(args) == 0 {
		return commandUsageError("ring render", "madari ring render <name> --client <target>")
	}
	if isHelpToken(args[0]) {
		printRingRenderHelp(a.stdout)
		return nil
	}
	name := strings.TrimSpace(args[0])

	fs := flag.NewFlagSet("ring render", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var target string
	fs.StringVar(&target, "client", "", "Target client the rendered config is for (required)")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printRingRenderHelp(a.stdout)
			return nil
		}
		return commandInputError("ring render", err.Error())
	}
	if fs.NArg() != 0 {
		return commandUnexpectedArgsError("ring render", fs.Args())
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return commandInputError("ring render", "--client is required")
	}
	renderTarget, ok := ringRenderTargets[target]
	if !ok {
		return commandInputError("ring render", fmt.Sprintf("unsupported render target %q (supported: %s)", target, strings.Join(supportedRingRenderTargets(), ", ")))
	}

	ring, err := a.store.GetRing(name)
	if err != nil {
		if errors.Is(err, registry.ErrRingNotFound) {
			return fmt.Errorf("ring %q not found", name)
		}
		return err
	}
	manifests, err := a.store.List()
	if err != nil {
		return err
	}
	byName := make(map[string]registry.Manifest, len(manifests))
	for _, manifest := range manifests {
		byName[manifest.Name] = manifest
	}

	// Render mutates nothing: no state, no refcounts, no config files. The
	// output is a self-contained config for ephemeral use (for example
	// `claude --mcp-config`); stdout carries only the JSON document.
	servers := map[string]renderedServer{}
	members := append([]string(nil), ring.Members...)
	sort.Strings(members)
	for _, member := range members {
		member = strings.TrimSpace(member)
		manifest, known := byName[member]
		switch {
		case !known:
			fmt.Fprintf(a.stderr, "warning: ring member %s no longer exists in the registry; omitted\n", member)
			continue
		case !manifest.Enabled:
			fmt.Fprintf(a.stderr, "warning: ring member %s is disabled; omitted\n", member)
			continue
		case !manifest.HasClient(target):
			fmt.Fprintf(a.stderr, "warning: ring member %s does not target %s; omitted\n", member, target)
			continue
		}
		if err := clients.ValidateCommandPath(manifest.Command); err != nil {
			fmt.Fprintf(a.stderr, "warning: ring member %s omitted: %s\n", member, err.Message)
			continue
		}

		entry := renderedServer{Command: manifest.Command}
		if len(manifest.Args) > 0 {
			entry.Args = append([]string(nil), manifest.Args...)
		}
		secret := make(map[string]bool, len(manifest.SecretEnv.Keys))
		for _, key := range manifest.SecretEnv.Keys {
			secret[strings.TrimSpace(key)] = true
		}
		var omitted []string
		for key, value := range manifest.Env {
			if secret[key] {
				omitted = append(omitted, key)
				continue
			}
			if entry.Env == nil {
				entry.Env = map[string]string{}
			}
			entry.Env[key] = value
		}
		if len(omitted) > 0 {
			sort.Strings(omitted)
			fmt.Fprintf(a.stderr, "warning: ring member %s: secret env values omitted (%s); provide them via the runtime environment\n", member, strings.Join(omitted, ", "))
		}
		servers[member] = entry
	}

	return renderTarget.render(a.stdout, servers)
}

func printRingRenderHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari ring render <name> --client <target>")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --client <target>          Target client the rendered config is for (required)")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Print a self-contained MCP config for the ring to stdout, for ephemeral")
	fmt.Fprintln(out, "  use such as `claude --mcp-config <(madari ring render research --client claude-code)`.")
	fmt.Fprintln(out, "  Members are filtered by client compatibility; disabled, missing, or")
	fmt.Fprintln(out, "  command-invalid members are omitted with a warning. Static values for")
	fmt.Fprintln(out, "  [secret_env] keys are never emitted — provide them via the runtime")
	fmt.Fprintln(out, "  environment. Render mutates no state and no refcounts.")
}

// parseRingOpArgs parses `<ring> <client> [flags]` shared by attach/detach.
func (a cliApp) parseRingOpArgs(command string, args []string, usage string, printHelp func(io.Writer)) (ringName, target, configPath, scope string, dryRun bool, err error) {
	if len(args) == 0 {
		return "", "", "", "", false, commandUsageError(command, usage)
	}
	if isHelpToken(args[0]) {
		printHelp(a.stdout)
		return "", "", "", "", false, errRingHelpShown
	}
	if len(args) < 2 {
		return "", "", "", "", false, commandUsageError(command, usage)
	}
	ringName = strings.TrimSpace(args[0])
	target = strings.TrimSpace(args[1])

	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&dryRun, "dry-run", false, "Preview changes without writing files")
	fs.StringVar(&configPath, "config-path", "", "Override client config path")
	fs.StringVar(&scope, "scope", "", "Target config scope for claude-code: project (default) or user")
	if parseErr := fs.Parse(args[2:]); parseErr != nil {
		if errors.Is(parseErr, flag.ErrHelp) {
			printHelp(a.stdout)
			return "", "", "", "", false, errRingHelpShown
		}
		return "", "", "", "", false, commandInputError(command, parseErr.Error())
	}
	if fs.NArg() != 0 {
		return "", "", "", "", false, commandUnexpectedArgsError(command, fs.Args())
	}

	scope = strings.TrimSpace(scope)
	if scope != "" {
		if target != claudecode.Target {
			return "", "", "", "", false, commandInputError(command, fmt.Sprintf("--scope is only supported for %s", claudecode.Target))
		}
		if scope != clients.ScopeProject && scope != clients.ScopeUser {
			return "", "", "", "", false, commandInputError(command, fmt.Sprintf("unknown scope %q (supported: %s, %s)", scope, clients.ScopeProject, clients.ScopeUser))
		}
	}
	if _, ok := syncAdapters[target]; !ok {
		return "", "", "", "", false, commandInputError(command, fmt.Sprintf("unsupported sync target %q (supported: %s)", target, strings.Join(supportedSyncTargets(), ", ")))
	}
	return ringName, target, configPath, scope, dryRun, nil
}

// errRingHelpShown signals the caller that help was printed and parsing
// stopped without an error.
var errRingHelpShown = errors.New("ring help shown")

func (a cliApp) ringOpStatePath(target, scope string) string {
	if scope == clients.ScopeUser {
		return a.managedUserStatePath(target)
	}
	return a.managedStatePath(target)
}

func (a cliApp) cmdRingAttach(args []string) error {
	ringName, target, configPath, scope, dryRun, err := a.parseRingOpArgs(
		"ring attach", args,
		"madari ring attach <ring> <client> [--scope project|user] [--dry-run] [--config-path <path>]",
		printRingAttachHelp,
	)
	if errors.Is(err, errRingHelpShown) {
		return nil
	}
	if err != nil {
		return err
	}

	ring, err := a.store.GetRing(ringName)
	if err != nil {
		if errors.Is(err, registry.ErrRingNotFound) {
			return fmt.Errorf("ring %q not found", ringName)
		}
		return err
	}
	manifests, err := a.store.List()
	if err != nil {
		return err
	}
	rings, err := a.store.ListRings()
	if err != nil {
		return err
	}

	a.warnRingMembers(ring, manifests, target)
	syncable, skipped := filterSyncableManifests(manifests, target)

	adapter := syncAdapters[target]
	result, err := adapter.AttachRing(ring, syncable, clients.SyncOptions{
		ConfigPath: configPath,
		StatePath:  a.ringOpStatePath(target, scope),
		Rings:      rings,
		Scope:      scope,
		DryRun:     dryRun,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "ring: %s\n", ringName)
	printSyncSummary(a.stdout, a.stderr, target, result.ConfigPath, result.DryRun, result.Added, result.Updated, result.Removed, result.Unchanged, skipped, result.Refused)
	return nil
}

func (a cliApp) cmdRingDetach(args []string) error {
	ringName, target, configPath, scope, dryRun, err := a.parseRingOpArgs(
		"ring detach", args,
		"madari ring detach <ring> <client> [--scope project|user] [--dry-run] [--config-path <path>]",
		printRingDetachHelp,
	)
	if errors.Is(err, errRingHelpShown) {
		return nil
	}
	if err != nil {
		return err
	}

	statePath := a.ringOpStatePath(target, scope)
	state, err := syncshared.LoadManagedState(statePath)
	if err != nil {
		return err
	}
	if !slices.Contains(syncshared.AttachedRings(state), ringName) {
		fmt.Fprintf(a.stdout, "ring %s is not attached to %s; nothing to do\n", ringName, target)
		return nil
	}

	rings, err := a.store.ListRings()
	if err != nil {
		return err
	}

	adapter := syncAdapters[target]
	result, err := adapter.DetachRing(ringName, clients.SyncOptions{
		ConfigPath: configPath,
		StatePath:  statePath,
		Rings:      rings,
		Scope:      scope,
		DryRun:     dryRun,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "ring: %s\n", ringName)
	printSyncSummary(a.stdout, a.stderr, target, result.ConfigPath, result.DryRun, result.Added, result.Updated, result.Removed, result.Unchanged, nil, result.Refused)
	return nil
}

type ringDeleteHolder struct {
	target string
	scope  string
}

func (h ringDeleteHolder) label() string {
	if h.scope == clients.ScopeUser {
		return h.target + " (user scope)"
	}
	return h.target
}

func (h ringDeleteHolder) detachCommand(ring string) string {
	command := fmt.Sprintf("madari ring detach %s %s", ring, h.target)
	if h.scope == clients.ScopeUser {
		command += " --scope user"
	}
	return command
}

func (a cliApp) cmdRingDelete(args []string) error {
	if len(args) == 0 {
		return commandUsageError("ring delete", "madari ring delete <name>")
	}
	if isHelpToken(args[0]) {
		printRingDeleteHelp(a.stdout)
		return nil
	}

	fs := flag.NewFlagSet("ring delete", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printRingDeleteHelp(a.stdout)
			return nil
		}
		return commandInputError("ring delete", err.Error())
	}
	if fs.NArg() != 0 {
		return commandUnexpectedArgsError("ring delete", fs.Args())
	}

	name := strings.TrimSpace(args[0])
	if _, err := a.store.GetRing(name); err != nil {
		if errors.Is(err, registry.ErrRingNotFound) {
			return fmt.Errorf("ring %q not found", name)
		}
		return err
	}

	holders, err := a.ringDeleteHolders(name)
	if err != nil {
		return err
	}
	if len(holders) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "ring %q is attached and cannot be deleted; detach it first:\n", name)
		for _, holder := range holders {
			fmt.Fprintf(&b, "  - %s: `%s`\n", holder.label(), holder.detachCommand(name))
		}
		b.WriteString("pass --config-path if the ring was attached to a custom config")
		return fmt.Errorf("%s", b.String())
	}

	if err := a.store.RemoveRing(name); err != nil {
		if errors.Is(err, registry.ErrRingNotFound) {
			return fmt.Errorf("ring %q not found", name)
		}
		return err
	}
	fmt.Fprintf(a.stdout, "deleted ring %s\n", name)
	return nil
}

func (a cliApp) ringDeleteHolders(name string) ([]ringDeleteHolder, error) {
	source := syncshared.RingSource(name)
	var holders []ringDeleteHolder
	for _, ref := range a.managedStateRefs() {
		state, err := syncshared.LoadManagedState(ref.path)
		if err != nil {
			return nil, err
		}
		for _, sources := range state {
			if !slices.Contains(sources, source) {
				continue
			}
			holders = append(holders, ringDeleteHolder{target: ref.target, scope: ref.scope})
			break
		}
	}
	return holders, nil
}

// warnRingMembers reports members that will be owned but not materialized.
func (a cliApp) warnRingMembers(ring registry.Ring, manifests []registry.Manifest, target string) {
	byName := make(map[string]registry.Manifest, len(manifests))
	for _, manifest := range manifests {
		byName[manifest.Name] = manifest
	}
	for _, member := range ring.Members {
		manifest, known := byName[strings.TrimSpace(member)]
		switch {
		case !known:
			fmt.Fprintf(a.stderr, "warning: ring member %s no longer exists in the registry; ownership recorded, nothing materialized\n", member)
		case !manifest.Enabled:
			fmt.Fprintf(a.stderr, "warning: ring member %s is disabled; ownership recorded, not materialized until re-enabled\n", member)
		case !manifest.HasClient(target):
			fmt.Fprintf(a.stderr, "warning: ring member %s does not target %s; ownership recorded, not materialized\n", member, target)
		}
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
	fmt.Fprintln(out, "  attach    Attach a ring to a client (materialize members)")
	fmt.Fprintln(out, "  detach    Detach a ring from a client")
	fmt.Fprintln(out, "  delete    Delete an unattached ring")
	fmt.Fprintln(out, "  render    Print a self-contained MCP config for a ring")
	fmt.Fprintln(out, "  status    Show attached rings and ownership per client")
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

func printRingAttachHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari ring attach <ring> <client> [--scope project|user] [--dry-run] [--config-path <path>]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --scope project|user       Claude Code only: target scope (default: project)")
	fmt.Fprintln(out, "  --dry-run                  Preview changes without writing files")
	fmt.Fprintln(out, "  --config-path <path>       Override client config path")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Record ring ownership for every member and materialize the eligible")
	fmt.Fprintln(out, "  ones into the client config. Attaching onto an entry madari does not")
	fmt.Fprintln(out, "  manage is refused, even when values match. Disabled or secret-refused")
	fmt.Fprintln(out, "  members stay owned but absent until they become eligible.")
}

func printRingDetachHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari ring detach <ring> <client> [--scope project|user] [--dry-run] [--config-path <path>]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --scope project|user       Claude Code only: target scope (default: project)")
	fmt.Fprintln(out, "  --dry-run                  Preview changes without writing files")
	fmt.Fprintln(out, "  --config-path <path>       Override client config path")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Release the ring's ownership; entries owned by nothing else leave the")
	fmt.Fprintln(out, "  client config. Works by name even if the ring file no longer exists.")
	fmt.Fprintln(out, "  Detaching a ring that is not attached is a no-op.")
}

func printRingDeleteHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari ring delete <name>")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Delete an unattached ring from the registry. Deletion is refused while")
	fmt.Fprintln(out, "  any client scope still records the ring as an ownership source; detach")
	fmt.Fprintln(out, "  it first, passing --config-path if it was attached to a custom config.")
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
