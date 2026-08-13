// from control_flow/join_envs.ts
//
// Exact join of two environments: a name present on both arms
// comes out holding both possibilities. A name missing from either
// arm is absent from the join — it did not survive both paths.
// ReplaceEnv writes that result back into the walker's env in place.

package walk

import "github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"

func JoinEnvs(a, b Env) Env {
	out := NewEnv()
	a.Range(func(name string, known abstractdomain.AbstractValue) bool {
		if other, ok := b.Get(name); ok {
			out.Set(name, abstractdomain.JoinKnown(known, other))
		}
		return true
	})
	return out
}

func ReplaceEnv(env, withEnv Env) {
	env.Range(func(name string, _ abstractdomain.AbstractValue) bool {
		env.Delete(name)
		return true
	})
	withEnv.Range(func(name string, known abstractdomain.AbstractValue) bool {
		env.Set(name, known)
		return true
	})
}
