// from control_flow/entry_env.ts
//
// What a function body knows at entry: stated annotations, then
// call-site joins, then the plain type (or opaque / residue). Judgment
// and hover both bind through this one function, so the two walks
// cannot disagree about a parameter.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/diagnose"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// InitialStateOfPlainParameter is initialStateOfPlainParameter in the
// TS source: the set a PLAIN type states at a parameter — syntax
// then host. Exported parameters whose type states nothing are
// opaque: callers live outside this file.
//
// TRUST GRADE. A parameter's declared type is a declaration-backed
// claim exactly as a resolved call's return type is
// (return_type_ground.go's typeGroundOf, which stamps every ground it
// hands back TrustLibrary so the claim's provenance survives to the
// sink). The parallel is not exact, though: a return type's ground
// comes from a callee tsc already CHECKED against real call sites
// (library grade — the claim rests on some OTHER declaration's own
// checking). A parameter's own annotation is instead READ, not
// proved by any execution or cross-call check — the same standing
// typeof_ground.go's GroundOfTypeofWord already stamps TrustSpec for
// (`typeof x === "number"` grades its ground TrustSpec, not
// TrustProved: a spec clause read correctly, never a kernel
// decision). The Rust twin (refinedpy/pyrefly/crates/refinedpy/src/
// check.rs, seed_parameters) states the identical rule in so many
// words: "known_set, TrustSpec — the annotation is read, not proved
// by execution." TrustSpec is the level chosen here for the same
// reason: a same-file `age: number` is closer to a cited language
// clause held for this one declaration than to a checked LIBRARY
// signature spanning other call sites.
//
// AtTrustLevel only ever LOWERS (MinTrustLevel): the ordinary case is
// a fresh TrustProved read (typereading's own recipes.go constructors
// all build TrustProved, so this stamp is what turns it into TrustSpec),
// but a read that already carries a weaker floor keeps that weaker
// floor rather than being raised. Applied at the two return points
// that carry a REAL type reading; Opaque and Residue are
// KindUnknown-shaped and carry no grade to touch either way
// (AtTrustLevel already no-ops there), so grading them explicitly
// would add nothing.
func InitialStateOfPlainParameter(p *program.CheckerProgram, parameter *ast.Node) abstractdomain.AbstractValue {
	unread := func() abstractdomain.AbstractValue {
		if ExportedFunctionParameter(p.Checker, parameter) {
			return abstractdomain.Opaque
		}
		return silence.Residue()
	}
	decl := parameter.AsParameterDeclaration()
	if !ast.IsIdentifier(decl.Name()) {
		return unread()
	}
	if decl.Type != nil {
		if held, ok := typereading.ReadDeclaredType(p.Checker, decl.Type, decl.Name()); ok {
			return abstractdomain.AtTrustLevel(held, abstractdomain.TrustSpec)
		}
		return unread()
	}
	if held, ok := typereading.ReadHostType(p.Checker, typereading.TypeAtLocation(p.Checker, decl.Name()), decl.Name(), 0); ok {
		return abstractdomain.AtTrustLevel(held, abstractdomain.TrustSpec)
	}
	return unread()
}

// entryStateMeet is the entry state a parameter with BOTH a call-site
// join and a plain declared reading holds: the meet of the two.
//
// The DIRECTION argument, which is why a meet and not the join alone:
// the call-site join summarizes what the callers this walk happened to
// see hand this parameter; the declared type states what EVERY caller
// may hand it. A join can only be NARROWER than the annotation — a
// caller handing a value the annotation excludes is tsc's own error,
// never a fact this walk inherits — so where the two disagree, the
// annotation is the ceiling and the join is the floor. Seeding from
// the join alone let a summary of some callers WIDEN a parameter past
// its own stated type: `name: string` entered its body carrying a
// function arm, and the plain-sort refutation downstream then read
// that parameter as a function.
//
// A kind UNION from the join is restricted arm by arm: an arm whose
// sort the declared reading provably excludes is a caller the
// annotation says cannot exist, so it drops. Every other pairing goes
// to abstractdomain.MeetKnown, which already answers the other side
// alone when one is unknown — the "where only one exists, it stands
// alone" case.
//
// EXACTNESS IS PRESERVED, and that is what makes this meet safe to run
// at the INLINE parameter binding too, where the caller's exact value is
// the whole point of the walk. The annotation is a ceiling, so a value
// inside it passes through unchanged: MeetKnown answers the other side
// alone against an unknown declared reading, keeps a KindValues side
// whole, and intersects two sets. Nothing the caller proved is dropped
// by the meet itself.
//
// The one thing stripped is a claim the declared type CANNOT carry:
// COMPLETENESS on an OPEN-MAP parameter. Completeness is earned by
// construction, never read off a type, and an `at` node whose type has
// an index signature (`Record<K, V>`, `{[k: string]: V}`) names no fixed
// key set — any key may be present and any key may be missing at some
// other call. The caller's keys and their values all stay; only the
// closed-world claim goes, so a missing-key read answers the residue
// instead of an exact `undefined`. The `at` node is the parameter
// declaration at a binding seam and nil where no declaration speaks.
func entryStateMeet(c *checker.Checker, at *ast.Node, fromCall, declared abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	fromCall = withoutOpenMapCompleteness(c, at, fromCall)
	if declared.Kind == abstractdomain.KindUnknown || fromCall.Kind == abstractdomain.KindUnknown {
		return abstractdomain.MeetKnown(fromCall, declared)
	}
	if fromCall.Kind == abstractdomain.KindKindUnion {
		demanded := abstractdomain.TypeofWordOfKnown(declared)
		if demanded == "" {
			// the declared reading pins no one sort, so no arm is provably
			// outside it — the join stands as it came
			return fromCall
		}
		var admitted []abstractdomain.AbstractValue
		for _, arm := range fromCall.Arms {
			if _, outside := SortOutsidePlain(arm, demanded); outside {
				continue
			}
			admitted = append(admitted, arm)
		}
		if len(admitted) == 0 {
			// every caller the join saw wears a sort the annotation
			// excludes: the join says nothing this walk can use, and the
			// declared type is the whole of what the body knows
			return declared
		}
		return abstractdomain.KindUnionOf(admitted)
	}
	return abstractdomain.MeetKnown(fromCall, declared)
}

// withoutOpenMapCompleteness clears the Complete flag on an object bound
// at a node whose declared type is an open map. The keys the value states
// and every key's value stay exactly as they came; the flag that says
// "these are ALL the keys" is the only thing removed, because an
// index-signature type says the opposite — some other call may hand this
// same parameter a key this one never wrote.
//
// It reaches the object under the absence wrapper and inside a kind
// union's arms, since a caller may hand `{…} | undefined` or a join of a
// literal with something else, and the flag rides on the object either
// way. A value that is not an object anywhere passes through untouched.
func withoutOpenMapCompleteness(c *checker.Checker, at *ast.Node, held abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	if c == nil || at == nil {
		return held
	}
	if !holdsCompleteObject(held) {
		return held
	}
	if !OpenMapAt(c, at) {
		return held
	}
	return clearedCompleteness(held)
}

// holdsCompleteObject is whether a value carries a complete object
// anywhere the walk would read a missing key off — asked before the type
// question so the checker is only consulted where the flag exists.
func holdsCompleteObject(held abstractdomain.AbstractValue) bool {
	switch held.Kind {
	case abstractdomain.KindObject:
		return held.Complete
	case abstractdomain.KindPossiblyUndefined, abstractdomain.KindPossiblyNaN:
		return held.Inner != nil && holdsCompleteObject(*held.Inner)
	case abstractdomain.KindKindUnion:
		for _, arm := range held.Arms {
			if holdsCompleteObject(arm) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// clearedCompleteness rewrites the same value with every object's
// Complete flag off, following the same shapes holdsCompleteObject reads.
func clearedCompleteness(held abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	switch held.Kind {
	case abstractdomain.KindObject:
		held.Complete = false
		return held
	case abstractdomain.KindPossiblyUndefined, abstractdomain.KindPossiblyNaN:
		if held.Inner == nil {
			return held
		}
		inner := clearedCompleteness(*held.Inner)
		held.Inner = &inner
		return held
	case abstractdomain.KindKindUnion:
		arms := make([]abstractdomain.AbstractValue, len(held.Arms))
		for i, arm := range held.Arms {
			arms[i] = clearedCompleteness(arm)
		}
		return abstractdomain.KindUnionOf(arms)
	default:
		return held
	}
}

// BindEntryEnvInput mirrors the destructured parameter of
// bindEntryEnv in the TS source.
type BindEntryEnvInput struct {
	P                     *program.CheckerProgram
	Env                   Env
	Parameters            []*ast.Node                             // ParameterDeclaration
	StatedParams          []*annotations.DeclaredRefinement       // nil entries allowed; nil slice means "no stated params"
	CallSiteInitialStates map[string]abstractdomain.AbstractValue // nil means "no call-site join"
	OnStated              func(name string, stated *annotations.DeclaredRefinement)
}

// BindEntryEnv is bindEntryEnv in the TS source: bind every
// parameter name into env. Stated outranks a call-site join; a join
// outranks a value already on the env (an enclosing callback pin);
// silence fills from the plain type. Destructuring reads stated or
// plain — the same source the body walk always used.
//
// CROSS-DIRECTORY: readDestructuring is bindings/destructuring.ts's
// readDestructuring — not yet ported (bindings/ is a concurrent
// wave-2 agent's file, joining this same package). Called here as a
// plain package-level function; the walk will not build until that
// agent lands it. abstractValueOfDeclared is
// assignability/declared_value.ts's function, likewise concurrent
// (assignability's FlowContext-reading files join this package per
// PORT.md) — called the same way.
func BindEntryEnv(input BindEntryEnvInput) {
	// bind funnels every write this function makes through one place,
	// so the log always shows exactly what the environment received —
	// this is where "Wide" first shows as plain number if it does.
	bind := func(name string, held abstractdomain.AbstractValue) {
		input.Env.Set(name, held)
		diagnose.LogIf(diagnose.EventOn("walk.entryEnv"), "walk.entryEnv", "param", name, "value", spellValue(held))
	}
	for i, parameter := range input.Parameters {
		var stated *annotations.DeclaredRefinement
		if input.StatedParams != nil && i < len(input.StatedParams) {
			stated = input.StatedParams[i]
		}
		decl := parameter.AsParameterDeclaration()
		if !ast.IsIdentifier(decl.Name()) {
			// STATED outranks a call-site join here exactly as it does for
			// an identifier parameter below (this function's own doc) — a
			// pattern with its own written annotation reads through it
			// alone, with no join lookup at all.
			if stated != nil {
				source := AbstractValueOfDeclared(*stated)
				ReadDestructuring(decl.Name(), source, func(name string, held abstractdomain.AbstractValue, at *ast.Node) {
					bind(name, silence.SeededBinding(input.P.Checker, held, at))
				})
				continue
			}
			// unstated: a destructured parameter's LEAVES are what a
			// call-site join binds (BindParameter already runs the
			// argument through this same ReadDestructuring at the call
			// site, per name) — read each leaf here exactly as the
			// identifier branch below reads its one name, meeting the join
			// against the pattern's own plain reading rather than
			// discarding it
			plain := InitialStateOfPlainParameter(input.P, parameter)
			ReadDestructuring(decl.Name(), plain, func(name string, held abstractdomain.AbstractValue, at *ast.Node) {
				if input.CallSiteInitialStates != nil {
					if fromCall, ok := input.CallSiteInitialStates[name]; ok {
						bind(name, entryStateMeet(input.P.Checker, at, fromCall, held))
						return
					}
				}
				bind(name, silence.SeededBinding(input.P.Checker, held, at))
			})
			continue
		}
		name := decl.Name().Text()
		if stated != nil {
			declaredValue := AbstractValueOfDeclared(*stated)
			// a STATED annotation is a CEILING, never a floor: where a
			// call-site join also reaches this parameter, the entry
			// value is the MEET of the two (entryStateMeet, the same
			// law the unstated branch below applies against the plain
			// type) rather than the declared reading alone. Without
			// this, a bare-keyword ground (annotations/type_node_sets.go's
			// grounding of `number`/`string`/`boolean`, which makes
			// `stated` non-nil for types that used to compile to
			// nothing) would make EVERY call-site-derived exact value
			// vanish behind the ground the moment its parameter carries
			// a written bare-keyword type -- exactly the widening this
			// checker forbids, just reached through the stated branch
			// instead of the unstated one.
			if input.CallSiteInitialStates != nil {
				if fromCall, ok := input.CallSiteInitialStates[name]; ok {
					bind(name, entryStateMeet(input.P.Checker, parameter, fromCall, declaredValue))
					if input.OnStated != nil {
						input.OnStated(name, stated)
					}
					continue
				}
			}
			bind(name, declaredValue)
			if input.OnStated != nil {
				input.OnStated(name, stated)
			}
			continue
		}
		if input.CallSiteInitialStates != nil {
			if fromCall, ok := input.CallSiteInitialStates[name]; ok {
				bind(name, entryStateMeet(input.P.Checker, parameter, fromCall, InitialStateOfPlainParameter(input.P, parameter)))
				continue
			}
		}
		if held, ok := input.Env.Get(name); ok && held.Kind != abstractdomain.KindUnknown {
			continue
		}
		// a `this` parameter is the receiver's type annotation, not a
		// value parameter: nothing fills it at a plain body walk, so
		// seeding it from the declared type would manufacture a typed
		// receiver the walk never saw. A caller that DID bind one
		// (ThisParameterCall's callEnv, a call-site state) is kept by
		// the branches above; unbound, the `this` readers keep their
		// honest floor (this_property_access.go's Opaque).
		if name == "this" {
			continue
		}
		bind(name, InitialStateOfPlainParameter(input.P, parameter))
	}
}
