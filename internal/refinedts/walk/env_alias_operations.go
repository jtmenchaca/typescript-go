// The three alias-aware environment writes, over Env.
//
// dataflowfacts holds these as functions over a plain
// map[string]abstractdomain.AbstractValue (Havoc, ForgetPlaceEntries,
// UpdateTracked). They cannot take an Env: walk imports dataflowfacts,
// so dataflowfacts cannot name walk's type. Materializing a map at
// each call would put the old whole-environment copy back on the
// hottest path there is — every assignment goes through one of these
// — so the env-typed forms live here and call only the public alias
// API (ClassOf, Invalidate). The map forms stay where they are for
// dataflowfacts' own callers and its tests.

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// HavocEnv is dataflowfacts.AliasClasses.Havoc over an Env: forget the
// whole class wherever it is bound.
func HavocEnv(aliases *dataflowfacts.AliasClasses, env Env, name string) {
	for member := range aliases.ClassOf(name) {
		if _, ok := env.Get(member); ok {
			env.Set(member, silence.Residue())
		}
		ForgetPlaceEntriesEnv(env, member)
	}
	aliases.Invalidate(name)
}

// ForgetPlaceEntriesEnv is dataflowfacts.ForgetPlaceEntries over an
// Env: drop every dotted place entry rooted at a name — the sweep
// every write and havoc of that name owes the place-value memory
// (assume_condition records the dotted keys; dots never appear in
// identifiers). The names are collected before any delete, so the
// visit never runs against a structure it is changing.
func ForgetPlaceEntriesEnv(env Env, root string) {
	prefix := root + "."
	var doomed []string
	env.Range(func(key string, _ abstractdomain.AbstractValue) bool {
		if strings.HasPrefix(key, prefix) {
			doomed = append(doomed, key)
		}
		return true
	})
	for _, key := range doomed {
		env.Delete(key)
	}
}

// UpdateTrackedEnv is dataflowfacts.UpdateTracked over an Env: replace
// a tracked name's knowledge, CLASS-AWARE. A same-shaped alias (it
// held the very value) takes the new one. An EMBEDDER — its knowledge
// holds the written reference under a key — sees that key either as
// the very object (the new value) or as another one (the old): their
// join covers both. Any other object-shaped class member is a
// CANDIDATE sharer (a branch-selected alias), and the join of its own
// state with the new value covers both cases too. Everything else
// forgets.
func UpdateTrackedEnv(
	aliases *dataflowfacts.AliasClasses,
	env Env,
	name string,
	next abstractdomain.AbstractValue,
) {
	aliases.Invalidate(name)
	ForgetPlaceEntriesEnv(env, name)
	held, ok := env.Get(name)
	if !ok {
		held = silence.Residue()
	}
	for member := range aliases.ClassOf(name) {
		memberKnown, ok := env.Get(member)
		if !ok {
			continue
		}
		if member == name || abstractdomain.SameKnown(memberKnown, held) {
			env.Set(member, next)
			continue
		}
		if memberKnown.Kind == abstractdomain.KindObject {
			keys := make([]abstractdomain.ObjectKey, len(memberKnown.Keys))
			copy(keys, memberKnown.Keys)
			embeds := false
			for i, key := range keys {
				if abstractdomain.SameKnown(key.Value, held) {
					keys[i].Value = abstractdomain.JoinKnown(held, next)
					embeds = true
				}
			}
			if embeds {
				env.Set(member, abstractdomain.KnownObject(keys, nil, false, abstractdomain.TrustProved, false))
				continue
			}
			env.Set(member, abstractdomain.JoinKnown(memberKnown, next))
			continue
		}
		env.Set(member, silence.Residue())
	}
}
