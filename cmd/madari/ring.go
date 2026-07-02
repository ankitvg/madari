package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/ankitvg/madari/internal/clients"
	"github.com/ankitvg/madari/internal/clients/syncshared"
	"github.com/ankitvg/madari/internal/registry"
)

func (a cliApp) cmdRing(args []string) error {
	if len(args) == 0 {
		return commandUsageError("ring", "madari ring <create|list|show|contract|attach|detach|delete|render|status> [options]")
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
	case "contract":
		return a.cmdRingContract(rest)
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
		return commandInputError("ring", fmt.Sprintf("unknown ring subcommand %q (supported: create, list, show, contract, attach, detach, delete, render, status)", sub))
	}
}

// ringTargetStatus is the per-(target, scope) view ring status reports.
type ringTargetStatus struct {
	target  string
	scope   string
	rings   []ringAttachment
	servers []serverSources
	skills  []serverSources
}

type ringAttachment struct {
	name           string
	exists         bool
	members        []string
	skills         []string
	owned          []string
	skillsOwned    []string
	pending        []string
	skillsPending  []string
	stale          []string
	skillsStale    []string
	missingMembers []string
	missingSkills  []string
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
	skills, err := a.store.ListSkills()
	if err != nil {
		return err
	}
	skillNames := make(map[string]bool, len(skills))
	for _, skill := range skills {
		skillNames[skill.Name] = true
	}

	statuses := []ringTargetStatus{}
	for _, ref := range a.managedStateRefs() {
		state, err := syncshared.LoadManagedState(ref.path)
		if err != nil {
			return err
		}
		skillState := map[string]skillAttachmentEntry{}
		if supportsSkillMaterialization(ref.target) {
			skillState, err = loadSkillAttachmentState(a.skillAttachmentStatePath(ref.target, ref.scope))
			if err != nil {
				return err
			}
		}
		ts := ringTargetStatus{target: ref.target, scope: ref.scope}

		for _, name := range unionStrings(syncshared.AttachedRings(state), attachedSkillRings(skillState)) {
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
				skillMembers := append([]string(nil), ring.Skills...)
				sort.Strings(skillMembers)
				attachment.skills = skillMembers
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
				skillMemberSet := make(map[string]bool, len(skillMembers))
				for _, skill := range skillMembers {
					skill = strings.TrimSpace(skill)
					skillMemberSet[skill] = true
					if skillAttachmentStateHasSource(skillState, skill, source) {
						attachment.skillsOwned = append(attachment.skillsOwned, skill)
					} else {
						attachment.skillsPending = append(attachment.skillsPending, skill)
					}
					if !skillNames[skill] {
						attachment.missingSkills = append(attachment.missingSkills, skill)
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
				for _, entry := range skillState {
					if !skillMemberSet[entry.Name] && skillAttachmentHasSource(entry, source) {
						attachment.skillsStale = appendUniqueName(attachment.skillsStale, entry.Name)
					}
				}
			}
			ts.rings = append(ts.rings, attachment)
		}

		serverNames := syncshared.MapKeys(state)
		sort.Strings(serverNames)
		for _, name := range serverNames {
			ts.servers = append(ts.servers, serverSources{name: name, sources: state[name]})
		}
		for _, entry := range sortedSkillAttachmentEntries(skillState) {
			ts.skills = append(ts.skills, serverSources{name: entry.Name, sources: entry.Sources})
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
				Skills:  make([]ringSkillJSON, 0, len(ts.skills)),
			}
			for _, att := range ts.rings {
				targetJSON.Rings = append(targetJSON.Rings, ringAttachmentJSON{
					Name:           att.name,
					Exists:         att.exists,
					Members:        nonNilStrings(att.members),
					Skills:         nonNilStrings(att.skills),
					Owned:          nonNilStrings(att.owned),
					SkillsOwned:    nonNilStrings(att.skillsOwned),
					Pending:        nonNilStrings(att.pending),
					SkillsPending:  nonNilStrings(att.skillsPending),
					Stale:          nonNilStrings(att.stale),
					SkillsStale:    nonNilStrings(att.skillsStale),
					MissingMembers: nonNilStrings(att.missingMembers),
					MissingSkills:  nonNilStrings(att.missingSkills),
				})
			}
			for _, server := range ts.servers {
				targetJSON.Servers = append(targetJSON.Servers, ringServerJSON{
					Name:    server.name,
					Sources: nonNilStrings(server.sources),
				})
			}
			for _, skill := range ts.skills {
				targetJSON.Skills = append(targetJSON.Skills, ringSkillJSON{
					Name:    skill.name,
					Sources: nonNilStrings(skill.sources),
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
		if len(ts.servers)+len(ts.skills) == 0 {
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
				if len(att.pending)+len(att.stale)+len(att.skillsPending)+len(att.skillsStale) > 0 {
					marker = "out-of-sync"
				}
				line := fmt.Sprintf("    %s [%s] members=%d owned=%d", att.name, marker, len(att.members), len(att.owned))
				if len(att.skills) > 0 || len(att.skillsOwned) > 0 || len(att.skillsPending) > 0 || len(att.skillsStale) > 0 {
					line += fmt.Sprintf(" skills=%d skill-owned=%d", len(att.skills), len(att.skillsOwned))
				}
				if len(att.pending) > 0 {
					line += fmt.Sprintf(" pending=%s", strings.Join(att.pending, ","))
				}
				if len(att.skillsPending) > 0 {
					line += fmt.Sprintf(" pending-skills=%s", strings.Join(att.skillsPending, ","))
				}
				if len(att.stale) > 0 {
					line += fmt.Sprintf(" stale=%s", strings.Join(att.stale, ","))
				}
				if len(att.skillsStale) > 0 {
					line += fmt.Sprintf(" stale-skills=%s", strings.Join(att.skillsStale, ","))
				}
				if len(att.pending)+len(att.stale)+len(att.skillsPending)+len(att.skillsStale) > 0 {
					line += fmt.Sprintf(" (run `%s`; pass --config-path if attached to a custom config)", syncHint)
				}
				if len(att.missingMembers) > 0 {
					line += fmt.Sprintf(" missing-from-registry=%s", strings.Join(att.missingMembers, ","))
				}
				if len(att.missingSkills) > 0 {
					line += fmt.Sprintf(" missing-skills-from-registry=%s", strings.Join(att.missingSkills, ","))
				}
				fmt.Fprintln(a.stdout, line)
			}
		}
		if len(ts.servers) > 0 {
			fmt.Fprintln(a.stdout, "  servers:")
			for _, server := range ts.servers {
				fmt.Fprintf(a.stdout, "    %s: %s\n", server.name, strings.Join(server.sources, ", "))
			}
		}
		if len(ts.skills) > 0 {
			fmt.Fprintln(a.stdout, "  skills:")
			for _, skill := range ts.skills {
				fmt.Fprintf(a.stdout, "    %s: %s\n", skill.name, strings.Join(skill.sources, ", "))
			}
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
	// `claude --mcp-config`); stdout carries only the rendered config.
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
		if manifest.IsRemote() {
			if !renderTarget.supportsRemote {
				fmt.Fprintf(a.stderr, "warning: ring member %s uses %s transport, which %s render does not support yet; omitted\n", member, manifest.TransportType(), target)
				continue
			}
			if len(manifest.Headers) > 0 {
				fmt.Fprintf(a.stderr, "warning: ring member %s: headers are not emitted for %s render\n", member, target)
			}
			if manifest.TimeoutMS > 0 {
				fmt.Fprintf(a.stderr, "warning: ring member %s: timeout_ms is not emitted for %s render\n", member, target)
			}
			servers[member] = renderedServer{
				Transport:     manifest.TransportType(),
				URL:           manifest.URL,
				Headers:       copyStringMap(manifest.Headers),
				TimeoutMS:     manifest.TimeoutMS,
				OAuthResource: manifest.OAuthResource,
			}
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
		entry.RuntimeEnvKeys = runtimeEnvKeys(manifest.RequiredEnv.Keys, manifest.SecretEnv.Keys)
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
	if len(ring.Skills) > 0 {
		fmt.Fprintf(a.stderr, "warning: ring skills are not included in MCP config render; use `madari ring attach %s %s` to materialize native skill files\n", ring.Name, target)
	}

	return renderTarget.render(a.stdout, servers)
}

func printRingRenderHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari ring render <name> --client <target>")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --client <target>          Target client the rendered config is for (required)")
	fmt.Fprintf(out, "                            Supported render targets: %s\n", strings.Join(supportedRingRenderTargets(), ", "))
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Print a self-contained MCP config for the ring to stdout, for ephemeral")
	fmt.Fprintln(out, "  use such as `claude --mcp-config <(madari ring render research --client claude-code)`.")
	fmt.Fprintln(out, "  Claude and Gemini targets emit JSON; Codex and Vibe targets emit TOML.")
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
	fs.StringVar(&scope, "scope", "", "Target config scope for supported clients: project (default) or user")
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
		if !targetSupportsUserScope(target) {
			return "", "", "", "", false, commandInputError(command, fmt.Sprintf("--scope is only supported for %s", strings.Join(userScopedSyncTargets(), ", ")))
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
	if err := validateRingSkillTarget("ring attach", ring, target); err != nil {
		return err
	}

	a.warnRingMembers(ring, manifests, target)
	syncable, skipped := filterSyncableManifests(manifests, target)

	adapter := syncAdapters[target]
	opts := clients.SyncOptions{
		ConfigPath: configPath,
		StatePath:  a.ringOpStatePath(target, scope),
		Rings:      rings,
		Scope:      scope,
	}
	if len(ring.Skills) > 0 && !dryRun {
		if _, err := a.attachRingSkills(ring, target, scope, true); err != nil {
			return err
		}
	}
	if len(ring.Members) > 0 && !dryRun {
		opts.DryRun = true
		if _, err := adapter.AttachRing(ring, syncable, opts); err != nil {
			return err
		}
	}

	result := clients.SyncResult{ConfigPath: "-", DryRun: dryRun}
	if len(ring.Members) > 0 {
		opts.DryRun = dryRun
		result, err = adapter.AttachRing(ring, syncable, opts)
		if err != nil {
			return err
		}
	}

	skillResult := skillAttachResult{DryRun: dryRun}
	if len(ring.Skills) > 0 {
		skillResult, err = a.attachRingSkills(ring, target, scope, dryRun)
		if err != nil {
			return err
		}
	}
	fmt.Fprintf(a.stdout, "ring: %s\n", ringName)
	if len(ring.Members) > 0 {
		printSyncSummary(a.stdout, a.stderr, target, result.ConfigPath, result.DryRun, result.Added, result.Updated, result.Removed, result.Unchanged, skipped, result.Refused)
	}
	printRingSkillSummary(a.stdout, skillResult)
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
	serverAttached := slices.Contains(syncshared.AttachedRings(state), ringName)
	skillAttached, err := a.ringSkillAttached(ringName, target, scope)
	if err != nil {
		return err
	}
	if !serverAttached && !skillAttached {
		fmt.Fprintf(a.stdout, "ring %s is not attached to %s; nothing to do\n", ringName, target)
		return nil
	}

	rings, err := a.store.ListRings()
	if err != nil {
		return err
	}

	adapter := syncAdapters[target]
	result := clients.SyncResult{ConfigPath: "-", DryRun: dryRun}
	if skillAttached && !dryRun {
		if _, err := a.detachSkillSourceAll(target, scope, syncshared.RingSource(ringName), true); err != nil {
			return err
		}
	}
	if serverAttached {
		result, err = adapter.DetachRing(ringName, clients.SyncOptions{
			ConfigPath: configPath,
			StatePath:  statePath,
			Rings:      rings,
			Scope:      scope,
			DryRun:     dryRun,
		})
		if err != nil {
			return err
		}
	}
	skillResult := skillAttachResult{DryRun: dryRun}
	if skillAttached {
		skillResult, err = a.detachSkillSourceAll(target, scope, syncshared.RingSource(ringName), dryRun)
		if err != nil {
			return err
		}
	}
	fmt.Fprintf(a.stdout, "ring: %s\n", ringName)
	if serverAttached {
		printSyncSummary(a.stdout, a.stderr, target, result.ConfigPath, result.DryRun, result.Added, result.Updated, result.Removed, result.Unchanged, nil, result.Refused)
	}
	printRingSkillSummary(a.stdout, skillResult)
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
	seen := map[string]struct{}{}
	for _, ref := range a.managedStateRefs() {
		state, err := syncshared.LoadManagedState(ref.path)
		if err != nil {
			return nil, err
		}
		for _, sources := range state {
			if !slices.Contains(sources, source) {
				continue
			}
			key := ref.target + "\x00" + ref.scope
			if _, exists := seen[key]; !exists {
				holders = append(holders, ringDeleteHolder{target: ref.target, scope: ref.scope})
				seen[key] = struct{}{}
			}
			break
		}
		if supportsSkillMaterialization(ref.target) {
			skillState, err := loadSkillAttachmentState(a.skillAttachmentStatePath(ref.target, ref.scope))
			if err != nil {
				return nil, err
			}
			for _, entry := range skillState {
				if !skillAttachmentHasSource(entry, source) {
					continue
				}
				key := ref.target + "\x00" + ref.scope
				if _, exists := seen[key]; !exists {
					holders = append(holders, ringDeleteHolder{target: ref.target, scope: ref.scope})
					seen[key] = struct{}{}
				}
				break
			}
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

func validateRingSkillTarget(command string, ring registry.Ring, target string) error {
	if len(ring.Skills) == 0 || supportsSkillMaterialization(target) {
		return nil
	}
	return commandInputError(command, fmt.Sprintf("ring %q contains skills but %s does not support skill materialization (supported skill targets: %s)", ring.Name, target, strings.Join(supportedSkillTargets(), ", ")))
}

func (a cliApp) attachRingSkills(ring registry.Ring, target, scope string, dryRun bool) (skillAttachResult, error) {
	result := skillAttachResult{DryRun: dryRun}
	source := syncshared.RingSource(ring.Name)
	skills := append([]string(nil), ring.Skills...)
	sort.Strings(skills)
	for _, skill := range skills {
		part, err := a.attachSkillSource(skill, target, scope, "", source, dryRun)
		if err != nil {
			return skillAttachResult{}, err
		}
		mergeSkillAttachResult(&result, part)
	}
	return result, nil
}

func (a cliApp) ringSkillAttached(ringName, target, scope string) (bool, error) {
	if !supportsSkillMaterialization(target) {
		return false, nil
	}
	state, err := loadSkillAttachmentState(a.skillAttachmentStatePath(target, scope))
	if err != nil {
		return false, err
	}
	source := syncshared.RingSource(ringName)
	for _, entry := range state {
		if skillAttachmentHasSource(entry, source) {
			return true, nil
		}
	}
	return false, nil
}

func (a cliApp) syncRingSkills(target, scope string, rings []registry.Ring, dryRun bool, attachedRingsBeforeSync []string) (skillAttachResult, error) {
	result := skillAttachResult{DryRun: dryRun}
	if !supportsSkillMaterialization(target) {
		return result, nil
	}
	serverState, err := syncshared.LoadManagedState(a.ringOpStatePath(target, scope))
	if err != nil {
		return skillAttachResult{}, err
	}
	skillState, err := loadSkillAttachmentState(a.skillAttachmentStatePath(target, scope))
	if err != nil {
		return skillAttachResult{}, err
	}

	byName := make(map[string]registry.Ring, len(rings))
	for _, ring := range rings {
		byName[ring.Name] = ring
	}
	for _, ringName := range unionStrings(attachedRingsBeforeSync, syncshared.AttachedRings(serverState), attachedSkillRings(skillState)) {
		ring, exists := byName[ringName]
		if !exists {
			continue
		}
		if err := validateRingSkillTarget("sync", ring, target); err != nil {
			return skillAttachResult{}, err
		}
		source := syncshared.RingSource(ringName)
		var stale []string
		for _, entry := range skillState {
			if skillAttachmentHasSource(entry, source) && !ring.HasSkill(entry.Name) {
				stale = appendUniqueName(stale, entry.Name)
			}
		}
		if len(stale) > 0 {
			part, err := a.detachSkillSourceNames(target, scope, source, stale, dryRun)
			if err != nil {
				return skillAttachResult{}, err
			}
			mergeSkillAttachResult(&result, part)
		}
		if len(ring.Skills) > 0 {
			part, err := a.attachRingSkills(ring, target, scope, dryRun)
			if err != nil {
				return skillAttachResult{}, err
			}
			mergeSkillAttachResult(&result, part)
		}
	}
	return result, nil
}

func attachedSkillRings(state map[string]skillAttachmentEntry) []string {
	seen := map[string]struct{}{}
	for _, entry := range state {
		for _, source := range entry.Sources {
			ring, ok := syncshared.RingNameFromSource(source)
			if !ok {
				continue
			}
			seen[ring] = struct{}{}
		}
	}
	rings := make([]string, 0, len(seen))
	for ring := range seen {
		rings = append(rings, ring)
	}
	sort.Strings(rings)
	return rings
}

func skillAttachmentStateHasSource(state map[string]skillAttachmentEntry, name, source string) bool {
	for _, entry := range state {
		if entry.Name == name && skillAttachmentHasSource(entry, source) {
			return true
		}
	}
	return false
}

func unionStrings(lists ...[]string) []string {
	seen := map[string]struct{}{}
	for _, list := range lists {
		for _, item := range list {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			seen[item] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for item := range seen {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func printRingSkillSummary(out io.Writer, result skillAttachResult) {
	if result.SkillsDir == "" && len(result.Added)+len(result.Updated)+len(result.Removed)+len(result.Unchanged) == 0 {
		return
	}
	if result.SkillsDir != "" {
		fmt.Fprintf(out, "skills dir: %s\n", result.SkillsDir)
	}
	fmt.Fprintf(out, "skills added: %s\n", formatNameList(result.Added))
	fmt.Fprintf(out, "skills updated: %s\n", formatNameList(result.Updated))
	fmt.Fprintf(out, "skills removed: %s\n", formatNameList(result.Removed))
	if len(result.Unchanged) > 0 {
		fmt.Fprintf(out, "skills unchanged: %s\n", formatNameList(result.Unchanged))
	}
	if len(result.Added)+len(result.Updated)+len(result.Removed) == 0 {
		fmt.Fprintln(out, "no skill changes")
	}
}

func (a cliApp) cmdRingCreate(args []string) error {
	if len(args) == 0 {
		return commandUsageError("ring create", "madari ring create <name> [--member <server> ...] [--skill <skill> ...] [--description <text>]")
	}
	if isHelpToken(args[0]) {
		printRingCreateHelp(a.stdout)
		return nil
	}
	name := strings.TrimSpace(args[0])

	fs := flag.NewFlagSet("ring create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var members stringList
	var skills stringList
	var description string
	fs.Var(&members, "member", "Ring member server name (repeatable)")
	fs.Var(&skills, "skill", "Ring member skill name (repeatable)")
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
		Skills:      append([]string(nil), skills...),
		Description: description,
	}
	if err := a.store.AddRing(ring); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "created ring %s with %d server member(s), %d skill member(s)\n", name, len(members), len(skills))
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
	fmt.Fprintln(a.stdout, "NAME\tMEMBERS\tSKILLS\tDESCRIPTION")
	for _, ring := range rings {
		members := append([]string(nil), ring.Members...)
		sort.Strings(members)
		skills := append([]string(nil), ring.Skills...)
		sort.Strings(skills)
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\n", ring.Name, strings.Join(members, ","), strings.Join(skills, ","), ring.Description)
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
	if len(ring.Skills) > 0 {
		fmt.Fprintln(a.stdout, "skills:")
		skills := append([]string(nil), ring.Skills...)
		sort.Strings(skills)
		for _, skill := range skills {
			fmt.Fprintf(a.stdout, "  - %s\n", skill)
		}
	}
	printRingContract(a.stdout, ring.Contract)
	return nil
}

func printRingContract(out io.Writer, contract *registry.RingContract) {
	if contract.Empty() {
		return
	}
	fmt.Fprintln(out, "contract:")
	if strings.TrimSpace(contract.Summary) != "" {
		fmt.Fprintf(out, "  summary: %s\n", contract.Summary)
	}
	printContractList(out, "good_for", contract.GoodFor)
	printContractList(out, "not_for", contract.NotFor)
	printContractList(out, "required_context", contract.RequiredContext)
	printContractList(out, "optional_context", contract.OptionalContext)
	printContractList(out, "expected_outputs", contract.ExpectedOutputs)
}

func printContractList(out io.Writer, label string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(out, "  %s:\n", label)
	for _, value := range values {
		fmt.Fprintf(out, "    - %s\n", value)
	}
}

func (a cliApp) cmdRingContract(args []string) error {
	if len(args) == 0 {
		return commandUsageError("ring contract", "madari ring contract <show|set|clear> <name> [options]")
	}
	if isHelpToken(args[0]) {
		printRingContractHelp(a.stdout)
		return nil
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "show":
		return a.cmdRingContractShow(rest)
	case "set":
		return a.cmdRingContractSet(rest)
	case "clear":
		return a.cmdRingContractClear(rest)
	default:
		return commandInputError("ring contract", fmt.Sprintf("unknown ring contract subcommand %q (supported: show, set, clear)", sub))
	}
}

func (a cliApp) cmdRingContractShow(args []string) error {
	if len(args) == 0 {
		return commandUsageError("ring contract show", "madari ring contract show <name>")
	}
	if isHelpToken(args[0]) {
		printRingContractShowHelp(a.stdout)
		return nil
	}
	name := strings.TrimSpace(args[0])

	fs := flag.NewFlagSet("ring contract show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printRingContractShowHelp(a.stdout)
			return nil
		}
		return commandInputError("ring contract show", err.Error())
	}
	if fs.NArg() != 0 {
		return commandUnexpectedArgsError("ring contract show", fs.Args())
	}

	ring, err := a.store.GetRing(name)
	if err != nil {
		if errors.Is(err, registry.ErrRingNotFound) {
			return fmt.Errorf("ring %q not found", name)
		}
		return err
	}
	if ring.Contract.Empty() {
		return fmt.Errorf("ring %q has no contract", name)
	}
	payload, err := registry.MarshalRingContract(*ring.Contract)
	if err != nil {
		return err
	}
	_, err = a.stdout.Write(payload)
	return err
}

func (a cliApp) cmdRingContractSet(args []string) error {
	if len(args) == 0 {
		return commandUsageError("ring contract set", "madari ring contract set <name> --file <path>")
	}
	if isHelpToken(args[0]) {
		printRingContractSetHelp(a.stdout)
		return nil
	}
	name := strings.TrimSpace(args[0])

	fs := flag.NewFlagSet("ring contract set", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var filePath string
	fs.StringVar(&filePath, "file", "", "Standalone contract TOML file to set (required)")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printRingContractSetHelp(a.stdout)
			return nil
		}
		return commandInputError("ring contract set", err.Error())
	}
	if fs.NArg() != 0 {
		return commandUnexpectedArgsError("ring contract set", fs.Args())
	}

	contract, err := readRingContractFile(filePath)
	if err != nil {
		return err
	}
	ring, err := a.store.GetRing(name)
	if err != nil {
		if errors.Is(err, registry.ErrRingNotFound) {
			return fmt.Errorf("ring %q not found", name)
		}
		return err
	}
	ring.Contract = &contract
	if err := a.store.SaveRing(ring); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "set contract for ring %s\n", name)
	return nil
}

func (a cliApp) cmdRingContractClear(args []string) error {
	if len(args) == 0 {
		return commandUsageError("ring contract clear", "madari ring contract clear <name>")
	}
	if isHelpToken(args[0]) {
		printRingContractClearHelp(a.stdout)
		return nil
	}
	name := strings.TrimSpace(args[0])

	fs := flag.NewFlagSet("ring contract clear", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printRingContractClearHelp(a.stdout)
			return nil
		}
		return commandInputError("ring contract clear", err.Error())
	}
	if fs.NArg() != 0 {
		return commandUnexpectedArgsError("ring contract clear", fs.Args())
	}

	ring, err := a.store.GetRing(name)
	if err != nil {
		if errors.Is(err, registry.ErrRingNotFound) {
			return fmt.Errorf("ring %q not found", name)
		}
		return err
	}
	ring.Contract = nil
	if err := a.store.SaveRing(ring); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "cleared contract for ring %s\n", name)
	return nil
}

func readRingContractFile(path string) (registry.RingContract, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return registry.RingContract{}, commandInputError("ring contract set", "--file is required")
	}
	cleanPath := filepath.Clean(path)
	payload, err := os.ReadFile(cleanPath)
	if err != nil {
		return registry.RingContract{}, fmt.Errorf("read contract file %q: %w", cleanPath, err)
	}
	contract, err := registry.ParseRingContract(payload)
	if err != nil {
		return registry.RingContract{}, fmt.Errorf("parse contract file %q: %w", cleanPath, err)
	}
	return contract, nil
}

func printRingHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari ring <subcommand> [options]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Subcommands:")
	fmt.Fprintln(out, "  create    Create a ring from registry servers and skills")
	fmt.Fprintln(out, "  list      List configured rings")
	fmt.Fprintln(out, "  show      Show one ring's members")
	fmt.Fprintln(out, "  contract  Show, set, or clear a ring contract")
	fmt.Fprintln(out, "  attach    Attach a ring to a client (materialize members)")
	fmt.Fprintln(out, "  detach    Detach a ring from a client")
	fmt.Fprintln(out, "  delete    Delete an unattached ring")
	fmt.Fprintln(out, "  render    Print a self-contained MCP config for a ring")
	fmt.Fprintln(out, "  status    Show attached rings and ownership per client")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Rings are named capability sets of MCP servers and skills. Server")
	fmt.Fprintln(out, "  members reference registry entries by name; skill members reference")
	fmt.Fprintln(out, "  managed skill entries by name.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Run `madari ring <subcommand> --help` for subcommand help.")
}

func printRingCreateHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari ring create <name> [--member <server> ...] [--skill <skill> ...] [--description <text>]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --member <server>          Ring member server name (repeatable)")
	fmt.Fprintln(out, "  --skill <skill>            Ring member skill name (repeatable)")
	fmt.Fprintln(out, "  --description <text>       Ring description")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Create a ring referencing existing registry servers and skills by name.")
	fmt.Fprintln(out, "  At least one server member or skill member is required.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Examples:")
	fmt.Fprintln(out, "  madari ring create research --member stewreads --member arxiv")
}

func printRingAttachHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari ring attach <ring> <client> [--scope project|user] [--dry-run] [--config-path <path>]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintf(out, "  --scope project|user       Supported by %s (default: project)\n", strings.Join(userScopedSyncTargets(), ", "))
	fmt.Fprintln(out, "  --dry-run                  Preview changes without writing files")
	fmt.Fprintln(out, "  --config-path <path>       Override client config path")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Record ring ownership for every server and skill member. Eligible")
	fmt.Fprintln(out, "  servers are materialized into the client config; skills are written")
	fmt.Fprintln(out, "  to the client's native skill directory when the target supports skills.")
	fmt.Fprintln(out, "  A ring with skills cannot attach to targets without skill support.")
	fmt.Fprintln(out, "  Attaching onto entries madari does not manage is refused.")
}

func printRingDetachHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari ring detach <ring> <client> [--scope project|user] [--dry-run] [--config-path <path>]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintf(out, "  --scope project|user       Supported by %s (default: project)\n", strings.Join(userScopedSyncTargets(), ", "))
	fmt.Fprintln(out, "  --dry-run                  Preview changes without writing files")
	fmt.Fprintln(out, "  --config-path <path>       Override client config path")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Release the ring's ownership; server config entries and skill files")
	fmt.Fprintln(out, "  owned by nothing else are removed. Works by name even if the ring file")
	fmt.Fprintln(out, "  no longer exists. Detaching a ring that is not attached is a no-op.")
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
	fmt.Fprintln(out, "  List configured rings with server members, skill members, and description.")
}

func printRingShowHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari ring show <name> [--json]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --json                     Emit JSON instead of text")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Show one ring's server members, skill members, and description.")
}

func printRingContractHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari ring contract <show|set|clear> <name> [options]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Subcommands:")
	fmt.Fprintln(out, "  show      Print a ring contract as standalone TOML")
	fmt.Fprintln(out, "  set       Replace a ring contract from a standalone TOML file")
	fmt.Fprintln(out, "  clear     Remove a ring contract")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Ring contracts are advisory metadata. These commands operate only on")
	fmt.Fprintln(out, "  standalone contract files and do not change ring members, skills,")
	fmt.Fprintln(out, "  attach state, sync behavior, or render output.")
}

func printRingContractShowHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari ring contract show <name>")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Print the ring contract as standalone TOML suitable for editing and")
	fmt.Fprintln(out, "  passing back to `madari ring contract set --file`.")
}

func printRingContractSetHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari ring contract set <name> --file <path>")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --file <path>              Standalone contract TOML file to set (required)")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Replace the ring contract with the complete contract in the file.")
	fmt.Fprintln(out, "  The file contains contract fields directly; do not include a")
	fmt.Fprintln(out, "  [contract] section or ring name/member fields.")
}

func printRingContractClearHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari ring contract clear <name>")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Remove the advisory contract from a ring. Members, skills, attach state,")
	fmt.Fprintln(out, "  sync behavior, and render output are unchanged.")
}
