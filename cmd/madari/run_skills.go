package main

import (
	"fmt"
	"path/filepath"

	"github.com/ankitvg/madari/internal/launch"
)

type runSkillMaterialization struct {
	Target    string
	SkillsDir string
	Skills    []string
}

func materializeRunSkills(artifact *launch.Artifact, runRoot string) (runSkillMaterialization, error) {
	if artifact == nil {
		return runSkillMaterialization{}, fmt.Errorf("immutable launch artifact is required")
	}
	packages := artifact.Skills()
	names := make([]string, 0, len(packages))
	for _, pkg := range packages {
		names = append(names, pkg.Skill.Name)
	}
	target := artifact.Target()
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

	for _, pkg := range packages {
		if err := writeMaterializedSkillPackage(filepath.Join(skillsRoot, pkg.Skill.Name), pkg.Files); err != nil {
			return result, fmt.Errorf("materialize skill %s: %w", pkg.Skill.Name, err)
		}
	}
	return result, nil
}
