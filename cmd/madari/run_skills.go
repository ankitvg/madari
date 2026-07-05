package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ankitvg/madari/internal/registry"
)

type runSkillMaterialization struct {
	Target    string
	SkillsDir string
	Skills    []string
}

func materializeRunSkills(store *registry.Store, target string, skills []runPlanSkill, runRoot string) (runSkillMaterialization, error) {
	names := runSkillNames(skills)
	result := runSkillMaterialization{
		Target: target,
		Skills: nonNilStrings(names),
	}
	if len(names) == 0 {
		return result, nil
	}

	ct, ok := clientTargetByName(target)
	if !ok {
		return result, fmt.Errorf("unsupported run target %q", target)
	}
	if !ct.skillRoots.runSupported() {
		return result, fmt.Errorf("%s run does not support run skill materialization", target)
	}
	skillsRoot, err := ct.skillRoots.resolveRunProject(runRoot)
	if err != nil {
		return result, fmt.Errorf("%s run skill root: %w", target, err)
	}
	result.SkillsDir = skillsRoot

	for _, name := range names {
		pkg, err := store.GetSkillPackage(name)
		if err != nil {
			return result, fmt.Errorf("skill %s: %w", name, err)
		}
		if err := writeMaterializedSkillPackage(filepath.Join(skillsRoot, name), pkg.Files); err != nil {
			return result, fmt.Errorf("materialize skill %s: %w", name, err)
		}
	}
	return result, nil
}

func runSkillNames(skills []runPlanSkill) []string {
	seen := map[string]struct{}{}
	for _, skill := range skills {
		name := strings.TrimSpace(skill.Name)
		if name != "" {
			seen[name] = struct{}{}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func appendRunSkillPrompt(b *strings.Builder, store *registry.Store, ringNames []string) error {
	skillNames := map[string]struct{}{}
	for _, name := range ringNames {
		ring, err := store.GetRing(name)
		if err != nil {
			return err
		}
		for _, skill := range ring.Skills {
			skill = strings.TrimSpace(skill)
			if skill != "" {
				skillNames[skill] = struct{}{}
			}
		}
	}
	if len(skillNames) == 0 {
		return nil
	}
	names := make([]string, 0, len(skillNames))
	for name := range skillNames {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Fprintln(b)
	fmt.Fprintln(b, "Selected skills:")
	for _, name := range names {
		skill, err := store.GetSkill(name)
		if err != nil {
			return err
		}
		fmt.Fprintf(b, "- %s", skill.Name)
		if strings.TrimSpace(skill.Description) != "" {
			fmt.Fprintf(b, ": %s", strings.TrimSpace(skill.Description))
		}
		fmt.Fprintln(b)
	}
	fmt.Fprintln(b, "Selected ring skills are materialized as project skills for this session.")
	return nil
}
