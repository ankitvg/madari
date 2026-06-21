package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ankitvg/madari/internal/clients"
	"github.com/ankitvg/madari/internal/registry"
)

func (a cliApp) cmdSkill(args []string) error {
	if len(args) == 0 {
		return commandUsageError("skill", "madari skill <add|update|remove|list|show|render|attach|detach> [options]")
	}
	if isHelpToken(args[0]) {
		printSkillHelp(a.stdout)
		return nil
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		return a.cmdSkillAdd(rest)
	case "update":
		return a.cmdSkillUpdate(rest)
	case "remove":
		return a.cmdSkillRemove(rest)
	case "list":
		return a.cmdSkillList(rest)
	case "show":
		return a.cmdSkillShow(rest)
	case "render":
		return a.cmdSkillRender(rest)
	case "attach":
		return a.cmdSkillAttach(rest)
	case "detach":
		return a.cmdSkillDetach(rest)
	default:
		return commandInputError("skill", fmt.Sprintf("unknown skill subcommand %q (supported: add, update, remove, list, show, render, attach, detach)", sub))
	}
}

func (a cliApp) cmdSkillAdd(args []string) error {
	if len(args) == 0 {
		return commandUsageError("skill add", "madari skill add <name> --file <path> [--description <text>]")
	}
	if isHelpToken(args[0]) {
		printSkillAddHelp(a.stdout)
		return nil
	}
	name := strings.TrimSpace(args[0])

	fs := flag.NewFlagSet("skill add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var filePath string
	var description string
	fs.StringVar(&filePath, "file", "", "Markdown skill file to copy into Madari (required)")
	fs.StringVar(&description, "description", "", "Skill description")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printSkillAddHelp(a.stdout)
			return nil
		}
		return commandInputError("skill add", err.Error())
	}
	if fs.NArg() != 0 {
		return commandUnexpectedArgsError("skill add", fs.Args())
	}

	content, err := readSkillSourceFile(filePath)
	if err != nil {
		return err
	}
	if err := a.store.AddSkill(registry.Skill{Name: name, Description: description}, content); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "added skill %s\n", name)
	return nil
}

func (a cliApp) cmdSkillUpdate(args []string) error {
	if len(args) == 0 {
		return commandUsageError("skill update", "madari skill update <name> --file <path> [--description <text>]")
	}
	if isHelpToken(args[0]) {
		printSkillUpdateHelp(a.stdout)
		return nil
	}
	name := strings.TrimSpace(args[0])

	fs := flag.NewFlagSet("skill update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var filePath string
	var description string
	fs.StringVar(&filePath, "file", "", "Markdown skill file to copy into Madari (required)")
	fs.StringVar(&description, "description", "", "Skill description")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printSkillUpdateHelp(a.stdout)
			return nil
		}
		return commandInputError("skill update", err.Error())
	}
	if fs.NArg() != 0 {
		return commandUnexpectedArgsError("skill update", fs.Args())
	}

	skill, err := a.store.GetSkill(name)
	if err != nil {
		if errors.Is(err, registry.ErrSkillNotFound) {
			return fmt.Errorf("skill %q not found", name)
		}
		return err
	}
	if flagWasProvided(fs, "description") {
		skill.Description = description
	}
	content, err := readSkillSourceFile(filePath)
	if err != nil {
		return err
	}
	if err := a.store.SaveSkill(skill, content); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "updated skill %s\n", name)
	return nil
}

func (a cliApp) cmdSkillRemove(args []string) error {
	if len(args) == 1 && isHelpToken(args[0]) {
		printSkillRemoveHelp(a.stdout)
		return nil
	}
	fs := flag.NewFlagSet("skill remove", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printSkillRemoveHelp(a.stdout)
			return nil
		}
		return commandInputError("skill remove", err.Error())
	}
	if fs.NArg() != 1 {
		return commandUsageError("skill remove", "madari skill remove <name>")
	}

	name := strings.TrimSpace(fs.Arg(0))
	if err := a.ensureSkillNotAttached(name); err != nil {
		return err
	}
	if err := a.store.RemoveSkill(name); err != nil {
		if errors.Is(err, registry.ErrSkillNotFound) {
			return fmt.Errorf("skill %q not found", name)
		}
		return err
	}
	fmt.Fprintf(a.stdout, "removed skill %s\n", name)
	return nil
}

func (a cliApp) cmdSkillList(args []string) error {
	if len(args) == 1 && isHelpToken(args[0]) {
		printSkillListHelp(a.stdout)
		return nil
	}
	fs := flag.NewFlagSet("skill list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Emit JSON instead of text")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printSkillListHelp(a.stdout)
			return nil
		}
		return commandInputError("skill list", err.Error())
	}
	if fs.NArg() != 0 {
		return commandUnexpectedArgsError("skill list", fs.Args())
	}

	skills, err := a.store.ListSkills()
	if err != nil {
		return err
	}

	if jsonOut {
		payload := skillListJSON{
			SchemaVersion: jsonSchemaVersion,
			Command:       "skill list",
			Skills:        make([]skillJSON, 0, len(skills)),
		}
		for _, skill := range skills {
			payload.Skills = append(payload.Skills, skillToJSON(skill, ""))
		}
		return writeJSON(a.stdout, payload)
	}

	if len(skills) == 0 {
		fmt.Fprintln(a.stdout, "no skills configured")
		return nil
	}
	fmt.Fprintln(a.stdout, "NAME\tDESCRIPTION")
	for _, skill := range skills {
		fmt.Fprintf(a.stdout, "%s\t%s\n", skill.Name, skill.Description)
	}
	return nil
}

func (a cliApp) cmdSkillShow(args []string) error {
	if len(args) == 0 {
		return commandUsageError("skill show", "madari skill show <name> [--json]")
	}
	if isHelpToken(args[0]) {
		printSkillShowHelp(a.stdout)
		return nil
	}
	name := strings.TrimSpace(args[0])

	fs := flag.NewFlagSet("skill show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Emit JSON instead of text")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printSkillShowHelp(a.stdout)
			return nil
		}
		return commandInputError("skill show", err.Error())
	}
	if fs.NArg() != 0 {
		return commandUnexpectedArgsError("skill show", fs.Args())
	}

	skill, err := a.store.GetSkill(name)
	if err != nil {
		if errors.Is(err, registry.ErrSkillNotFound) {
			return fmt.Errorf("skill %q not found", name)
		}
		return err
	}
	contentPath, err := a.store.SkillContentPath(skill.Name)
	if err != nil {
		return err
	}

	if jsonOut {
		return writeJSON(a.stdout, skillShowJSON{
			SchemaVersion: jsonSchemaVersion,
			Command:       "skill show",
			Skill:         skillToJSON(skill, contentPath),
		})
	}

	fmt.Fprintf(a.stdout, "name: %s\n", skill.Name)
	if strings.TrimSpace(skill.Description) != "" {
		fmt.Fprintf(a.stdout, "description: %s\n", skill.Description)
	}
	fmt.Fprintf(a.stdout, "content: %s\n", contentPath)
	return nil
}

func (a cliApp) cmdSkillRender(args []string) error {
	if len(args) == 0 {
		return commandUsageError("skill render", "madari skill render <name> [--client <target>]")
	}
	if isHelpToken(args[0]) {
		printSkillRenderHelp(a.stdout)
		return nil
	}
	name := strings.TrimSpace(args[0])

	fs := flag.NewFlagSet("skill render", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var target string
	fs.StringVar(&target, "client", "", "Render native SKILL.md for target client")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printSkillRenderHelp(a.stdout)
			return nil
		}
		return commandInputError("skill render", err.Error())
	}
	if fs.NArg() != 0 {
		return commandUnexpectedArgsError("skill render", fs.Args())
	}

	skill, err := a.store.GetSkill(name)
	if err != nil {
		if errors.Is(err, registry.ErrSkillNotFound) {
			return fmt.Errorf("skill %q not found", name)
		}
		return err
	}
	content, err := a.store.GetSkillContent(name)
	if err != nil {
		if errors.Is(err, registry.ErrSkillNotFound) {
			return fmt.Errorf("skill %q content not found", name)
		}
		return err
	}
	target = strings.TrimSpace(target)
	if target != "" {
		if _, err := skillTargetByName(target); err != nil {
			return err
		}
		rendered, err := renderClientSkill(skill, content)
		if err != nil {
			return err
		}
		_, err = a.stdout.Write(rendered.Content)
		return err
	}
	_, err = a.stdout.Write(content)
	return err
}

func (a cliApp) cmdSkillAttach(args []string) error {
	name, target, scope, skillsDir, dryRun, err := a.parseSkillAttachArgs(
		"skill attach",
		args,
		"madari skill attach <name> <client> [--scope project|user] [--skills-dir <dir>] [--dry-run]",
		printSkillAttachHelp,
	)
	if errors.Is(err, errSkillHelpShown) {
		return nil
	}
	if err != nil {
		return err
	}

	result, err := a.attachSkill(name, target, scope, skillsDir, dryRun)
	if err != nil {
		return err
	}
	printSkillAttachSummary(a.stdout, target, scope, result)
	return nil
}

func (a cliApp) cmdSkillDetach(args []string) error {
	name, target, scope, skillsDir, dryRun, err := a.parseSkillAttachArgs(
		"skill detach",
		args,
		"madari skill detach <name> <client> [--scope project|user] [--skills-dir <dir>] [--dry-run]",
		printSkillDetachHelp,
	)
	if errors.Is(err, errSkillHelpShown) {
		return nil
	}
	if err != nil {
		return err
	}

	result, err := a.detachSkill(name, target, scope, skillsDir, dryRun)
	if err != nil {
		return err
	}
	printSkillAttachSummary(a.stdout, target, scope, result)
	return nil
}

var errSkillHelpShown = errors.New("skill help shown")

func (a cliApp) parseSkillAttachArgs(command string, args []string, usage string, printHelp func(io.Writer)) (name, target, scope, skillsDir string, dryRun bool, err error) {
	if len(args) > 0 && isHelpToken(args[0]) {
		printHelp(a.stdout)
		return "", "", "", "", false, errSkillHelpShown
	}
	if len(args) < 2 {
		return "", "", "", "", false, commandUsageError(command, usage)
	}
	name = strings.TrimSpace(args[0])
	target = strings.TrimSpace(args[1])

	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&scope, "scope", "", "Skill target scope: project (default) or user")
	fs.StringVar(&skillsDir, "skills-dir", "", "Override skill root directory")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview changes without writing files")
	if err := fs.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printHelp(a.stdout)
			return "", "", "", "", false, errSkillHelpShown
		}
		return "", "", "", "", false, commandInputError(command, err.Error())
	}
	if fs.NArg() != 0 {
		return "", "", "", "", false, commandUnexpectedArgsError(command, fs.Args())
	}
	scope = strings.TrimSpace(scope)
	if scope != "" && scope != clients.ScopeProject && scope != clients.ScopeUser {
		return "", "", "", "", false, commandInputError(command, fmt.Sprintf("unknown scope %q (supported: %s, %s)", scope, clients.ScopeProject, clients.ScopeUser))
	}
	if _, err := skillTargetByName(target); err != nil {
		return "", "", "", "", false, err
	}
	return name, target, scope, skillsDir, dryRun, nil
}

func readSkillSourceFile(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, commandInputError("skill", "--file is required")
	}
	cleanPath := filepath.Clean(path)
	content, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("read skill file %q: %w", cleanPath, err)
	}
	if strings.TrimSpace(string(content)) == "" {
		return nil, fmt.Errorf("skill file %q is empty", cleanPath)
	}
	return content, nil
}

func flagWasProvided(fs *flag.FlagSet, name string) bool {
	provided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			provided = true
		}
	})
	return provided
}

func printSkillHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari skill <subcommand> [options]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Subcommands:")
	fmt.Fprintln(out, "  add       Add a managed skill from a Markdown file")
	fmt.Fprintln(out, "  update    Replace a managed skill's Markdown content")
	fmt.Fprintln(out, "  remove    Remove a managed skill")
	fmt.Fprintln(out, "  list      List configured skills")
	fmt.Fprintln(out, "  show      Show one skill's metadata")
	fmt.Fprintln(out, "  render    Print one skill's Markdown instructions")
	fmt.Fprintln(out, "  attach    Materialize a skill into a client skill directory")
	fmt.Fprintln(out, "  detach    Remove a materialized Madari-owned client skill")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Skills are standalone Markdown instructions that teach a model about")
	fmt.Fprintln(out, "  a domain or workflow. Generic render prints the managed Markdown exactly;")
	fmt.Fprintln(out, "  client render and attach synthesize native SKILL.md frontmatter.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Run `madari skill <subcommand> --help` for subcommand help.")
}

func printSkillAddHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari skill add <name> --file <path> [--description <text>]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --file <path>              Markdown skill file to copy into Madari (required)")
	fmt.Fprintln(out, "  --description <text>       Skill description")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Add a standalone skill by copying a non-empty Markdown file into")
	fmt.Fprintln(out, "  Madari's managed skills directory.")
}

func printSkillUpdateHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari skill update <name> --file <path> [--description <text>]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --file <path>              Markdown skill file to copy into Madari (required)")
	fmt.Fprintln(out, "  --description <text>       Replace the skill description")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Replace the managed Markdown content for an existing skill. The")
	fmt.Fprintln(out, "  current description is preserved unless --description is passed.")
}

func printSkillRemoveHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari skill remove <name>")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Remove a standalone skill from Madari's registry. Refuses while the")
	fmt.Fprintln(out, "  skill is still attached to any client skill directory.")
}

func printSkillListHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari skill list [--json]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --json                     Emit JSON instead of text")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  List configured standalone skills.")
}

func printSkillShowHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari skill show <name> [--json]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --json                     Emit JSON instead of text")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Show one skill's metadata and managed Markdown file path.")
}

func printSkillRenderHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari skill render <name> [--client <target>]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --client <target>          Render native SKILL.md for a client")
	fmt.Fprintf(out, "                            Supported skill targets: %s\n", strings.Join(supportedSkillTargets(), ", "))
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Print the managed Markdown instructions for a standalone skill to")
	fmt.Fprintln(out, "  stdout exactly. With --client, synthesize native SKILL.md frontmatter")
	fmt.Fprintln(out, "  for preview or piping. Render mutates no state and writes no client files.")
}

func printSkillAttachHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari skill attach <name> <client> [--scope project|user] [--skills-dir <dir>] [--dry-run]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --scope project|user       Skill scope (default: project)")
	fmt.Fprintln(out, "  --skills-dir <dir>         Override skill root directory")
	fmt.Fprintln(out, "  --dry-run                  Preview changes without writing files")
	fmt.Fprintf(out, "                            Supported skill targets: %s\n", strings.Join(supportedSkillTargets(), ", "))
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Materialize a managed skill as <skills-dir>/<name>/SKILL.md using")
	fmt.Fprintln(out, "  client-native YAML frontmatter. Refuses to overwrite unmanaged files.")
}

func printSkillDetachHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  madari skill detach <name> <client> [--scope project|user] [--skills-dir <dir>] [--dry-run]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  --scope project|user       Skill scope (default: project)")
	fmt.Fprintln(out, "  --skills-dir <dir>         Override skill root directory")
	fmt.Fprintln(out, "  --dry-run                  Preview changes without writing files")
	fmt.Fprintf(out, "                            Supported skill targets: %s\n", strings.Join(supportedSkillTargets(), ", "))
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Description:")
	fmt.Fprintln(out, "  Remove a Madari-owned materialized SKILL.md. Detach refuses to delete")
	fmt.Fprintln(out, "  files modified since Madari last wrote them.")
}
