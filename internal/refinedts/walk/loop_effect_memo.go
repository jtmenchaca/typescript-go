// Read-once for the loop solver's SILENT passes — no TS twin (Go-only
// mechanism, same precedent as call_site_snapshot_fill.go). A loop
// fixpoint walks its body several times to settle a candidate, and a
// NESTED loop re-solves in full on every enclosing pass — the same
// (loop, entry state, step input) walking again and again was the
// getTicks/getEquidistantTicks amplifier. The step image is a
// function of the loop and the state it steps from, so an identical
// state replays the remembered image. Only silent passes remember or
// replay; the one checked pass always walks, because its walk reports.

package walk

import (
	"strings"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

type loopEffectKey struct {
	loop *ast.Node
	// state is spell(solve entry) + \x1d + spell(step input): the
	// transfers, element binding, and condition rows all derive from
	// the entry state, so the pair pins everything the step reads.
	state string
}

var (
	loopEffectMu sync.Mutex
	loopEffects  = map[*program.CheckerProgram]map[loopEffectKey]Env{}
)

// spellEnvForMemo spells a whole environment deterministically —
// sorted names, each value through the memo speller. ("", false)
// where any value refuses a spelling; the caller walks instead.
func spellEnvForMemo(env Env) (string, bool) {
	// Range already visits in sorted name order, so the spelling is
	// deterministic without a separate collect-and-sort.
	var b strings.Builder
	spellable := true
	env.Range(func(name string, v abstractdomain.AbstractValue) bool {
		spelled, ok := abstractdomain.SpellForMemoKey(v)
		if !ok {
			spellable = false
			return false
		}
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(spelled)
		b.WriteByte('\x1e')
		return true
	})
	if !spellable {
		return "", false
	}
	return b.String(), true
}

// loopEffectStateOf builds the memo state for one silent step. ""
// where the step input cannot be spelled.
func loopEffectStateOf(entrySpell string, fromEnv Env) string {
	fromSpell, ok := spellEnvForMemo(fromEnv)
	if !ok {
		return ""
	}
	return entrySpell + "\x1d" + fromSpell
}

// rememberedLoopEffect answers a previously walked step image, copied
// for the caller to mutate.
func rememberedLoopEffect(p *program.CheckerProgram, loop *ast.Node, state string) (Env, bool) {
	loopEffectMu.Lock()
	held := loopEffects[p]
	var image Env
	if held != nil {
		image = held[loopEffectKey{loop: loop, state: state}]
	}
	loopEffectMu.Unlock()
	if image == nil {
		tracing.Count("loop.effect.miss", 0)
		return nil, false
	}
	tracing.Count("loop.effect.hit", 0)
	return image.Clone(), true
}

// rememberLoopEffect records one walked step image, copied so the
// caller's onward mutation never reaches the store.
func rememberLoopEffect(p *program.CheckerProgram, loop *ast.Node, state string, image Env) {
	loopEffectMu.Lock()
	held := loopEffects[p]
	if held == nil {
		held = map[loopEffectKey]Env{}
		loopEffects[p] = held
	}
	held[loopEffectKey{loop: loop, state: state}] = image.Clone()
	loopEffectMu.Unlock()
}
