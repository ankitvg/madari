package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/ankitvg/madari/internal/clients"
	"github.com/ankitvg/madari/internal/registry"
)

const runDryRunOnlyMessage = "madari run execution is only implemented for codex; pass --dry-run to inspect the launch plan"

type runPlanJSON struct {
	SchemaVersion   int             `json:"schema_version"`
	Command         string          `json:"command"`
	Target          string          `json:"target"`
	Rings           []string        `json:"rings"`
	Ready           bool            `json:"ready"`
	RunnerAvailable bool            `json:"runner_available"`
	PromptProvided  bool            `json:"prompt_provided"`
	Servers         []runPlanServer `json:"servers"`
	Skills          []runPlanSkill  `json:"skills"`
	Env             []runPlanEnv    `json:"env"`
	Warnings        []string        `json:"warnings"`
	Errors          []string        `json:"errors"`
}

type runLaunchPlan struct {
	Target          string
	Rings           []string
	Ready           bool
	RunnerAvailable bool
	PromptProvided  bool
	Servers         []runPlanServer
	Skills          []runPlanSkill
	Env             []runPlanEnv
	Warnings        []string
	Errors          []string
}

type runPlanServer struct {
	Name       string   `json:"name"`
	Transport  string   `json:"transport"`
	Endpoint   string   `json:"endpoint"`
	Status     string   `json:"status"`
	Auth       string   `json:"auth,omitempty"`
	RuntimeEnv []string `json:"runtime_env"`
	Rings      []string `json:"rings"`
	Issues     []string `json:"issues"`
}

type runPlanSkill struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Rings  []string `json:"rings"`
	Issues []string `json:"issues"`
}

type runPlanEnv struct {
	Key     string   `json:"key"`
	Present bool     `json:"present"`
	Servers []string `json:"servers"`
}

func (a cliApp) cmdRun(args []string) error {
	if len(args) == 0 {
		return commandUsageError("run", "madari run <client> --ring <ring> [--ring <ring> ...] --dry-run -- <prompt>")
	}
	if isHelpToken(args[0]) {
		printRunHelp(a.stdout)
		return nil
	}

	target := strings.TrimSpace(args[0])
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var rings stringList
	var dryRun bool
	var jsonOutput bool
	fs.Var(&rings, "ring", "Ring to include in the launch plan (repeatable)")
	fs.BoolVar(&dryRun, "dry-run", false, "Inspect the launch plan without starting the client")
	fs.BoolVar(&jsonOutput, "json", false, "Emit JSON instead of text")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printRunHelp(a.stdout)
			return nil
		}
		return commandInputError("run", err.Error())
	}

	if target == "" {
		return commandInputError("run", "client is required")
	}
	if jsonOutput && !dryRun {
		return commandInputError("run", "--json is only supported with --dry-run")
	}
	normalizedRings := normalizedRingNames(rings)
	if len(normalizedRings) == 0 {
		return commandInputError("run", "--ring is required")
	}
	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		return commandInputError("run", "prompt is required")
	}

	plan, err := a.buildRunPlan(target, normalizedRings, prompt)
	if err != nil {
		return err
	}
	if dryRun && jsonOutput {
		if err := writeJSON(a.stdout, plan.toJSON()); err != nil {
			return err
		}
	} else if dryRun {
		printRunPlan(a.stdout, plan)
	}
	if !plan.Ready {
		if !dryRun {
			printRunPlan(a.stdout, plan)
		}
		return commandInputError("run", "launch plan is not ready")
	}
	if dryRun {
		return nil
	}
	executor, ok := runExecutorForTarget(target)
	if !ok {
		return commandInputError("run", runDryRunOnlyMessage)
	}
	if err := executor(a, plan, prompt); err != nil {
		return err
	}
	return nil
}

func (a cliApp) buildRunPlan(target string, ringNames []string, prompt string) (runLaunchPlan, error) {
	plan := runLaunchPlan{
		Target:          target,
		Rings:           nonNilStrings(normalizedRingNames(ringNames)),
		RunnerAvailable: false,
		PromptProvided:  strings.TrimSpace(prompt) != "",
	}
	addPlanError := func(message string) {
		plan.Errors = append(plan.Errors, message)
	}

	if _, ok := clientTargetByName(target); !ok {
		addPlanError(fmt.Sprintf("unsupported run target %q (supported: %s)", target, strings.Join(sortedClientTargetNames(), ", ")))
		plan.finish()
		return plan, nil
	}
	rt, runnerImplemented := runTargetByName(target)
	if runnerImplemented {
		plan.RunnerAvailable = true
		if strings.TrimSpace(rt.executable) != "" {
			if _, err := exec.LookPath(rt.executable); err != nil {
				plan.RunnerAvailable = false
				addPlanError(fmt.Sprintf("%s executable not found in PATH; install the %s CLI before running this target", rt.executable, target))
			}
		}
		if rt.planPreflight != nil {
			if err := rt.planPreflight(); err != nil {
				addPlanError(err.Error())
			}
		}
	}
	for ring, count := range countStrings(plan.Rings) {
		if count > 1 {
			addPlanError(fmt.Sprintf("duplicate ring %q", ring))
		}
	}

	manifests, err := a.store.List()
	if err != nil {
		return runLaunchPlan{}, err
	}
	manifestByName := make(map[string]registry.Manifest, len(manifests))
	for _, manifest := range manifests {
		manifestByName[manifest.Name] = manifest
	}

	serverRings := map[string][]string{}
	skillRings := map[string][]string{}
	for _, name := range plan.Rings {
		ring, err := a.store.GetRing(name)
		if err != nil {
			if errors.Is(err, registry.ErrRingNotFound) {
				addPlanError(fmt.Sprintf("ring %q not found", name))
				continue
			}
			return runLaunchPlan{}, err
		}
		for _, member := range ring.Members {
			member = strings.TrimSpace(member)
			if member != "" {
				serverRings[member] = appendUniqueName(serverRings[member], name)
			}
		}
		for _, skill := range ring.Skills {
			skill = strings.TrimSpace(skill)
			if skill != "" {
				skillRings[skill] = appendUniqueName(skillRings[skill], name)
			}
		}
	}

	envRequirements := map[string][]string{}
	serverNames := sortedStringSliceMapKeys(serverRings)
	for _, name := range serverNames {
		rings := serverRings[name]
		server := runPlanServer{
			Name:       name,
			Status:     "ready",
			RuntimeEnv: []string{},
			Rings:      nonNilStrings(append([]string(nil), rings...)),
			Issues:     []string{},
		}
		manifest, exists := manifestByName[name]
		if !exists {
			server.Status = "blocked"
			server.Issues = append(server.Issues, "server is missing from the registry")
			plan.Servers = append(plan.Servers, server)
			addPlanError(fmt.Sprintf("ring member %s no longer exists in the registry", name))
			continue
		}
		server.Transport = manifest.TransportType()
		server.Endpoint = manifestEndpoint(manifest)
		server.Auth = runPlanAuth(manifest)
		server.RuntimeEnv = runtimeEnvKeys(manifest.RequiredEnv.Keys, manifest.SecretEnv.Keys)
		if manifest.RequiresBearerTokenEnv() {
			server.RuntimeEnv = runtimeEnvKeys(server.RuntimeEnv, []string{manifest.BearerTokenEnvVar})
		}

		if !manifest.Enabled {
			server.Issues = append(server.Issues, "server is disabled")
		}
		if !manifest.HasClient(target) {
			server.Issues = append(server.Issues, fmt.Sprintf("server does not target %s", target))
		}
		if manifest.IsRemote() {
			if detail, unsupported := unsupportedRemoteForTarget(manifest, target); unsupported {
				if detail.Auth != "" {
					server.Issues = append(server.Issues, fmt.Sprintf("requires %s, which %s run does not support yet", detail.Auth, target))
				} else {
					server.Issues = append(server.Issues, fmt.Sprintf("uses %s transport, which %s run does not support yet", detail.Transport, target))
				}
			}
			if runnerImplemented {
				if secretNames := manifest.SecretHeaderNames(); len(secretNames) > 0 {
					server.Issues = append(server.Issues, fmt.Sprintf("static secret header values cannot be passed to %s run: %s", target, strings.Join(secretNames, ", ")))
				}
			}
		} else if err := clients.ValidateCommandPath(manifest.Command); err != nil {
			server.Issues = append(server.Issues, err.Message)
		}

		for _, key := range server.RuntimeEnv {
			envRequirements[key] = appendUniqueName(envRequirements[key], name)
			if !runtimeEnvPresent(key) {
				server.Issues = append(server.Issues, fmt.Sprintf("runtime env %s is missing", key))
			}
		}

		if len(server.Issues) > 0 {
			server.Status = "blocked"
			for _, issue := range server.Issues {
				addPlanError(fmt.Sprintf("server %s: %s", name, issue))
			}
		}
		plan.Servers = append(plan.Servers, server)
	}

	if len(skillRings) > 0 && !supportsRunSkillMaterialization(target) {
		addPlanError(fmt.Sprintf("%s does not support run skill materialization (supported run skill targets: %s)", target, strings.Join(supportedRunSkillTargets(), ", ")))
	}
	skillNames := sortedStringSliceMapKeys(skillRings)
	for _, name := range skillNames {
		skill := runPlanSkill{
			Name:   name,
			Status: "ready",
			Rings:  nonNilStrings(append([]string(nil), skillRings[name]...)),
			Issues: []string{},
		}
		if _, err := a.store.GetSkillPackage(name); err != nil {
			if errors.Is(err, registry.ErrSkillNotFound) {
				skill.Issues = append(skill.Issues, "skill is missing from the registry")
			} else {
				skill.Issues = append(skill.Issues, fmt.Sprintf("skill package cannot be materialized: %v", err))
			}
		}
		if !supportsRunSkillMaterialization(target) {
			skill.Issues = append(skill.Issues, fmt.Sprintf("%s does not support run skill materialization", target))
		}
		if len(skill.Issues) > 0 {
			skill.Status = "blocked"
			for _, issue := range skill.Issues {
				addPlanError(fmt.Sprintf("skill %s: %s", name, issue))
			}
		}
		plan.Skills = append(plan.Skills, skill)
	}

	envKeys := sortedStringSliceMapKeys(envRequirements)
	for _, key := range envKeys {
		plan.Env = append(plan.Env, runPlanEnv{
			Key:     key,
			Present: runtimeEnvPresent(key),
			Servers: nonNilStrings(envRequirements[key]),
		})
	}

	plan.finish()
	return plan, nil
}

func (p *runLaunchPlan) finish() {
	p.Errors = nonNilStrings(sortedUniqueStrings(p.Errors))
	p.Warnings = nonNilStrings(sortedUniqueStrings(p.Warnings))
	p.Ready = len(p.Errors) == 0
}

func (p runLaunchPlan) toJSON() runPlanJSON {
	return runPlanJSON{
		SchemaVersion:   jsonSchemaVersion,
		Command:         "run",
		Target:          p.Target,
		Rings:           nonNilStrings(p.Rings),
		Ready:           p.Ready,
		RunnerAvailable: p.RunnerAvailable,
		PromptProvided:  p.PromptProvided,
		Servers:         nonNilRunPlanServers(p.Servers),
		Skills:          nonNilRunPlanSkills(p.Skills),
		Env:             nonNilRunPlanEnv(p.Env),
		Warnings:        nonNilStrings(p.Warnings),
		Errors:          nonNilStrings(p.Errors),
	}
}

func runPlanAuth(manifest registry.Manifest) string {
	switch {
	case manifest.RequiresBearerTokenEnv():
		return "bearer_token_env_var"
	case strings.TrimSpace(manifest.OAuthResource) != "":
		return "oauth_resource"
	default:
		return ""
	}
}

func normalizedRingNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func countStrings(names []string) map[string]int {
	counts := map[string]int{}
	for _, name := range names {
		counts[name]++
	}
	return counts
}

func sortedUniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		seen[value] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sortedStringSliceMapKeys(values map[string][]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func runtimeEnvPresent(key string) bool {
	value, ok := os.LookupEnv(key)
	return ok && strings.TrimSpace(value) != ""
}

func nonNilRunPlanServers(servers []runPlanServer) []runPlanServer {
	if servers == nil {
		return []runPlanServer{}
	}
	return servers
}

func nonNilRunPlanSkills(skills []runPlanSkill) []runPlanSkill {
	if skills == nil {
		return []runPlanSkill{}
	}
	return skills
}

func nonNilRunPlanEnv(env []runPlanEnv) []runPlanEnv {
	if env == nil {
		return []runPlanEnv{}
	}
	return env
}

func printRunPlan(out io.Writer, plan runLaunchPlan) {
	status := "ready"
	if !plan.Ready {
		status = "blocked"
	}
	fmt.Fprintf(out, "run target: %s\n", plan.Target)
	fmt.Fprintf(out, "rings: %s\n", formatNameList(plan.Rings))
	fmt.Fprintf(out, "status: %s\n", status)
	if plan.RunnerAvailable {
		fmt.Fprintln(out, "runner: available")
	} else {
		fmt.Fprintln(out, "runner: unavailable")
	}
	fmt.Fprintln(out)

	fmt.Fprintln(out, "servers:")
	if len(plan.Servers) == 0 {
		fmt.Fprintln(out, "  -")
	} else {
		for _, server := range plan.Servers {
			detail := fmt.Sprintf("  %s %s %s", server.Name, server.Status, server.Transport)
			if server.Auth != "" {
				detail += " auth=" + server.Auth
			}
			if len(server.RuntimeEnv) > 0 {
				detail += " env=" + formatNameList(server.RuntimeEnv)
			}
			if len(server.Rings) > 0 {
				detail += " rings=" + formatNameList(server.Rings)
			}
			fmt.Fprintln(out, detail)
			for _, issue := range server.Issues {
				fmt.Fprintf(out, "    issue: %s\n", issue)
			}
		}
	}

	fmt.Fprintln(out, "skills:")
	if len(plan.Skills) == 0 {
		fmt.Fprintln(out, "  -")
	} else {
		for _, skill := range plan.Skills {
			detail := fmt.Sprintf("  %s %s", skill.Name, skill.Status)
			if len(skill.Rings) > 0 {
				detail += " rings=" + formatNameList(skill.Rings)
			}
			fmt.Fprintln(out, detail)
			for _, issue := range skill.Issues {
				fmt.Fprintf(out, "    issue: %s\n", issue)
			}
		}
	}

	fmt.Fprintln(out, "runtime env:")
	if len(plan.Env) == 0 {
		fmt.Fprintln(out, "  -")
	} else {
		for _, env := range plan.Env {
			status := "missing"
			if env.Present {
				status = "present"
			}
			fmt.Fprintf(out, "  %s %s", env.Key, status)
			if len(env.Servers) > 0 {
				fmt.Fprintf(out, " servers=%s", formatNameList(env.Servers))
			}
			fmt.Fprintln(out)
		}
	}

	fmt.Fprintln(out, "prompt: provided")
	if plan.RunnerAvailable {
		fmt.Fprintln(out, "execution: available when --dry-run is omitted")
	} else {
		fmt.Fprintln(out, "execution: not implemented for this target; dry-run only")
	}
	if len(plan.Warnings) > 0 {
		fmt.Fprintln(out, "warnings:")
		for _, warning := range plan.Warnings {
			fmt.Fprintf(out, "  %s\n", warning)
		}
	}
	if len(plan.Errors) > 0 {
		fmt.Fprintln(out, "errors:")
		for _, issue := range plan.Errors {
			fmt.Fprintf(out, "  %s\n", issue)
		}
	}
}

func printRunHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari run <client> --ring <ring> [--ring <ring> ...] [--dry-run] -- <prompt>")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --ring <ring>             Ring to include in the launch plan (repeatable)")
	fmt.Fprintln(out, "  --dry-run                 Inspect the launch plan without starting the client")
	fmt.Fprintln(out, "  --json                    Emit JSON instead of text (requires --dry-run)")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Plan or start an ephemeral client launch from one or more rings. Codex")
	fmt.Fprintln(out, "  execution starts `codex exec --ephemeral --ignore-user-config")
	fmt.Fprintln(out, "  --skip-git-repo-check --sandbox read-only`, clears inherited MCP")
	fmt.Fprintln(out, "  config, and injects selected ring MCP servers as required config")
	fmt.Fprintln(out, "  overrides from an isolated working root and materializes selected")
	fmt.Fprintln(out, "  ring skills into that temporary root. Stdio servers keep the original")
	fmt.Fprintln(out, "  working directory and caller HOME/USERPROFILE. Other clients are dry-run")
	fmt.Fprintln(out, "  only for now.")
	fmt.Fprintln(out, "  Run never writes client config, managed state, or permanent skill files.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Examples:")
	fmt.Fprintln(out, "  madari run codex --ring cloudsql-readonly -- \"Who are the top 5 ebook creators?\"")
	fmt.Fprintln(out, "  madari run codex --ring cloudsql-readonly --ring research --dry-run --json -- \"Summarize the target plan\"")
}
