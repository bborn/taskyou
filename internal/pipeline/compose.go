package pipeline

import (
	"fmt"
	"strings"
)

// effectiveInstruction returns the full body template for a step. Built-in steps
// carry a hand-written Instruction and use it verbatim. Custom (YAML) steps carry
// only a Prompt — what the step should do — and the git handoff is composed from
// the step's position in the DAG, so authors never hand-write the commit/push/PR
// boilerplate (and can't get the parallel-branch handling subtly wrong).
func effectiveInstruction(def Definition, stepName string) string {
	step, ok := def.step(stepName)
	if !ok {
		return ""
	}
	if strings.TrimSpace(step.Instruction) != "" {
		return step.Instruction
	}
	return composeInstruction(def, step)
}

// step looks a step up by name.
func (d Definition) step(name string) (Step, bool) {
	for _, s := range d.Steps {
		if s.Name == name {
			return s, true
		}
	}
	return Step{}, false
}

// dependents returns the steps that depend on the named step.
func (d Definition) dependents(name string) []Step {
	var out []Step
	for _, s := range d.Steps {
		for _, dep := range s.Deps {
			if dep == name {
				out = append(out, s)
			}
		}
	}
	return out
}

// hasParallelPeer reports whether the step runs at the same time as another step,
// so its output must go to its own branch to avoid clobbering the peer. Two steps
// are parallel if they share a dependency; multiple root steps (no deps) are all
// parallel entry points.
func (d Definition) hasParallelPeer(s Step) bool {
	if len(s.Deps) == 0 {
		return len(d.Roots()) > 1
	}
	depSet := make(map[string]bool, len(s.Deps))
	for _, dep := range s.Deps {
		depSet[dep] = true
	}
	for _, other := range d.Steps {
		if other.Name == s.Name {
			continue
		}
		for _, dep := range other.Deps {
			if depSet[dep] {
				return true
			}
		}
	}
	return false
}

// composeInstruction builds a custom step's body: an intro, the author's prompt,
// then a git-handoff section derived from the DAG.
//
//   - A step is on the shared branch {{branch}}. If it has a parallel peer it
//     pushes its output to its OWN branch ({{branch}}-{{stepslug}}) so peers can't
//     clobber each other; otherwise it commits and pushes {{branch}}.
//   - It reads each dependency that ran in parallel from that dep's own branch.
//   - A sink step (nothing depends on it) opens the PR; every other step must not.
func composeInstruction(def Definition, step Step) string {
	var b strings.Builder

	fmt.Fprintf(&b, "This is the **%s** step of the automated **%s** workflow, running on the shared branch `{{branch}}`.\n\n", step.Name, def.Name)
	b.WriteString("## Goal\n{{goal}}\n\n")
	b.WriteString("## Your task\n")
	b.WriteString(strings.TrimSpace(step.Prompt))
	b.WriteString("\n\n## Handoff (do this so the workflow can continue)\n")

	// Inputs: dependencies that ran in parallel published to their own branches.
	var parallelDeps []Step
	for _, depName := range step.Deps {
		if dep, ok := def.step(depName); ok && def.hasParallelPeer(dep) {
			parallelDeps = append(parallelDeps, dep)
		}
	}
	if len(parallelDeps) > 0 {
		b.WriteString("- Your inputs were produced in parallel and pushed to their own branches; read each:\n")
		for _, dep := range parallelDeps {
			slug := slugify(dep.Name, 40)
			fmt.Fprintf(&b, "    - **%s** → `git fetch origin && git show origin/{{branch}}-%s:<its output file>` (branch `{{branch}}-%s`)\n", dep.Name, slug, slug)
		}
	}

	// Output: own branch when parallel, shared branch otherwise.
	if def.hasParallelPeer(step) {
		slug := slugify(step.Name, 40)
		b.WriteString("- You run in parallel with a sibling step, so push your output to YOUR OWN branch (one commit, one push — no rebase, no clobber):\n")
		fmt.Fprintf(&b, "    `git add <your output file> && git commit -m \"%s\" && git push origin HEAD:{{branch}}-%s`\n", slug, slug)
		b.WriteString("  Do NOT push to `{{branch}}` itself.\n")
	} else {
		b.WriteString("- Commit your work and push the shared branch:\n")
		b.WriteString("    `git add -A && git commit -m \"{{stepslug}}: ...\" && git push origin HEAD:{{branch}}`\n")
		b.WriteString("  (If you are on a detached HEAD, run `git checkout -B {{branch}}` first.)\n")
	}

	// PR: only the sink step opens it.
	if len(def.dependents(step.Name)) == 0 {
		b.WriteString("- You are the final step: open a pull request with `gh pr create` summarizing the change. The workflow then parks in 'blocked' for a human to merge.\n")
	} else {
		b.WriteString("- Do NOT open a pull request — a later step does that.\n")
	}
	b.WriteString("- Then call `taskyou_complete` with a one-line summary. That advances the workflow.")

	return b.String()
}
