package foundry

import (
	"encoding/json"
	"path/filepath"
)

const memuSynthesisInstruction = `Compare the existing skill/ proposals with the evidence in export/manifest.json and its referenced JSONL files. This is the cross-session synthesis pass, not another single-session job.
Read the relevant evidence from at least two independent projects. Different project names alone do not establish different domains: verify the actual tasks and tool results.
If independent domains support a reusable practice, propose a general SKILL.md under skill/ with stable citations from each domain. Keep domain-specific skills separate and state the limits. Otherwise make no general proposal.
Normalize existing skill proposals into portable skill/<name>/SKILL.md directories with name/description frontmatter and supporting scripts under each skill's scripts/ directory. Move existing implementations rather than duplicating them; preserve their behavior. Update relative links. The tree must remain usable when mirrored under a project's .agents/skills/. A general skill must stand alone without requiring another project's domain skill files.
Propose supported project-scoped AGENTS.md additions under skill/rules/ as separate Markdown files containing the scoped rule and compact citations, without copying unrelated human rules or proposal-status boilerplate into the rule. Preserve human files; never edit real project policy.
Review verification scripts and run real positive and negative controls where safe. Record observed commands and results, not invented results. Search existing proposal names and relevant project skills before creating duplicates. A justified no-op is valid.
Write proposals only; do not adopt, index, modify settings or invoke memu-codex commit.`

// memU's upstream jobs learn from one session each. A final pass can compare
// independent projects without pretending that one session establishes generality.
func appendMemuSynthesisJob(base string, jobs []memuJobDTO) ([]memuJobDTO, error) {
	if len(jobs) == 0 {
		return jobs, nil
	}
	existing := -1
	for index, job := range jobs {
		if job.Kind == "synthesis" {
			if job.Content == memuSynthesisInstruction {
				return jobs, nil
			}
			existing = index
			break
		}
	}
	raw, err := readMemuFile(filepath.Join(base, "export", "manifest.json"), 1<<20)
	if err != nil {
		return nil, err
	}
	var manifest memuExportManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return nil, err
	}
	projects := map[string]bool{}
	for _, session := range manifest.Sessions {
		for _, citation := range session.Citations {
			if session.Project != "" && citation.Exported && citation.Role == "user" {
				projects[session.Project] = true
			}
		}
	}
	if len(projects) < 2 {
		return jobs, nil
	}
	path := filepath.Join(base, "jobs", "workbench-synthesis.txt")
	if err := writePrivateFile(path, []byte(memuSynthesisInstruction)); err != nil {
		return nil, err
	}
	job := memuJobDTO{Path: path, Content: memuSynthesisInstruction, Kind: "synthesis", Status: "queued"}
	if existing >= 0 {
		jobs[existing] = job
		return jobs, nil
	}
	return append(jobs, job), nil
}
