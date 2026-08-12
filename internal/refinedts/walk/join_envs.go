// from control_flow/join_envs.ts
//
// Exact join of two environments: a name present on both arms
// comes out holding both possibilities. A name missing from either
// arm is absent from the join — it did not survive both paths.
// ReplaceEnv writes that result back into the walker's env in place.

package walk

import "github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"

func JoinEnvs(a, b Env) Env {
	out := Env{}
	for name, known := range a {
		if other, ok := b[name]; ok {
			out[name] = abstractdomain.JoinKnown(known, other)
		}
	}
	return out
}

func ReplaceEnv(env, withEnv Env) {
	for name := range env {
		delete(env, name)
	}
	for name, known := range withEnv {
		env[name] = known
	}
}
