package pipeline

import "fmt"

// A kind can be built from other kinds: a step may reference a kind (Step.Kind),
// and if that kind is itself a workflow, its steps are inlined here — so `ship`
// can be composed from `plan-code-review` + `qa` + `deploy` without repeating them.
// Flatten does that inlining at build time (a pure preprocessor), so the executor
// only ever sees a flat DAG of leaf steps and needs no notion of nesting.

// maxKindDepth bounds how deep kinds may nest, a backstop against runaway
// expansion on top of the cycle check.
const maxKindDepth = 16

// ResolveFunc returns the kind definition for a name and whether it exists. It is
// how Flatten looks up a step's referenced kind (a YAML kind, a built-in, or — via
// the convention bridge — a DB task type surfaced as a single-task kind).
type ResolveFunc func(name string) (Definition, bool)

// Flatten expands every step that references a multi-step kind into that kind's
// sub-DAG, returning a workflow whose steps are all leaves. Each leaf keeps its
// referenced Kind, so the built task's Type applies that kind's instructions. A
// single-task kind is returned unchanged. Cycles across kinds and nesting past
// maxKindDepth are rejected.
func Flatten(def Definition, resolve ResolveFunc) (Definition, error) {
	if def.IsSingle() {
		return def, nil
	}
	steps, err := flattenSteps(def.Steps, resolve, []string{def.Name}, 0)
	if err != nil {
		return Definition{}, err
	}
	out := def
	out.Steps = steps
	if err := out.validate(); err != nil {
		return Definition{}, fmt.Errorf("kind %q after flattening: %w", def.Name, err)
	}
	return out, nil
}

// flattenSteps turns one level of steps into a flat leaf-step list, inlining any
// step whose Kind resolves to a multi-step kind. stack is the chain of kind names
// currently being expanded (for cycle detection); depth guards runaway nesting.
func flattenSteps(steps []Step, resolve ResolveFunc, stack []string, depth int) ([]Step, error) {
	if depth > maxKindDepth {
		return nil, fmt.Errorf("kinds nested deeper than %d levels (cycle or runaway nesting?)", maxKindDepth)
	}

	// exit maps each input step name to the leaf-step name(s) that represent its
	// completion — itself when it stays a leaf, or the sub-workflow's sink(s) when
	// it's inlined. Downstream deps on that step are rewritten to these.
	exit := make(map[string][]string, len(steps))
	// external records, per emitted step, the input-level dep names still to be
	// resolved through exit (a leaf's own deps, and an inlined sub-root's deps).
	var out []Step
	var external [][]string

	emit := func(s Step, ext []string) {
		out = append(out, s)
		external = append(external, ext)
	}

	for _, s := range steps {
		sub, isWorkflow, err := resolveWorkflowKind(s, resolve, stack)
		if err != nil {
			return nil, err
		}
		if !isWorkflow {
			// Leaf: an inline-prompt step, or a step running a single-task kind (its
			// Kind stays so the task's Type applies that kind's instructions). Its deps
			// are input-level names, resolved in the second pass.
			leaf := s
			leaf.Deps = nil
			emit(leaf, s.Deps)
			exit[s.Name] = []string{s.Name}
			continue
		}

		// Inline the sub-workflow (already flat) under this step, namespacing its step
		// names so they can't collide, and wiring it into the parent DAG:
		//   - sub-roots inherit this step's deps (external, resolved in pass two)
		//   - sub-internal deps are kept, just prefixed
		//   - this step's dependents will point at the sub-sinks (via exit)
		prefix := s.Name + "/"
		for _, ss := range sub.Steps {
			ns := ss
			ns.Name = prefix + ss.Name
			var ext []string
			if len(ss.Deps) == 0 {
				ext = s.Deps // a sub-root starts once this step's deps are met
				ns.Deps = nil
			} else {
				ns.Deps = prefixAll(prefix, ss.Deps)
			}
			// Carry the parent step's executor/model as defaults for sub-steps that
			// didn't set their own, so `kind:` refs can still be routed from above.
			if ns.Executor == "" || ns.Executor == "claude" {
				if s.Executor != "" {
					ns.Executor = s.Executor
				}
			}
			if ns.Model == "" && s.Model != "" {
				ns.Model = s.Model
			}
			emit(ns, ext)
		}
		exit[s.Name] = prefixAll(prefix, sinkNames(sub))
	}

	// Second pass: resolve every external (input-level) dep through exit.
	for i := range out {
		deps := out[i].Deps
		for _, d := range external[i] {
			deps = append(deps, exit[d]...)
		}
		out[i].Deps = dedupeStrings(deps)
	}
	return out, nil
}

// resolveWorkflowKind reports whether step s references a kind that is a workflow
// (has steps), returning that kind fully flattened. A missing or single-task kind
// is not a workflow (the step stays a leaf). Referencing a kind already on the
// expansion stack is a cycle.
func resolveWorkflowKind(s Step, resolve ResolveFunc, stack []string) (Definition, bool, error) {
	if s.Kind == "" {
		return Definition{}, false, nil
	}
	def, ok := resolve(s.Kind)
	if !ok || def.IsSingle() {
		return Definition{}, false, nil
	}
	for _, name := range stack {
		if name == s.Kind {
			return Definition{}, false, fmt.Errorf("kind cycle: %s → %s", joinArrow(stack), s.Kind)
		}
	}
	flat, err := flattenSteps(def.Steps, resolve, append(append([]string{}, stack...), s.Kind), len(stack))
	if err != nil {
		return Definition{}, false, err
	}
	def.Steps = flat
	return def, true, nil
}

// sinkNames returns the steps nothing depends on (a workflow's terminal steps).
func sinkNames(d Definition) []string {
	depended := make(map[string]bool)
	for _, s := range d.Steps {
		for _, dep := range s.Deps {
			depended[dep] = true
		}
	}
	var sinks []string
	for _, s := range d.Steps {
		if !depended[s.Name] {
			sinks = append(sinks, s.Name)
		}
	}
	return sinks
}

func prefixAll(prefix string, names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = prefix + n
	}
	return out
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func joinArrow(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += " → "
		}
		out += n
	}
	return out
}
