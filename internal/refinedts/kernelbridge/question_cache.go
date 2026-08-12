// The question cache: the kernel is a PURE decider, so identical
// canonical bytes give the identical answer — and an answer is a
// theorem about those bytes, true on every machine forever, so the
// store persists, salted by the kernel artifact's identity.
// Split from kernel_bridge.ts per the v2 tree.
//
// The TS store lives at ~/.cache/refinedts/questions-v1.json. This Go
// twin uses a DIFFERENT file name — questions-go-v1.json — so the two
// checkers never share a store (their salts differ anyway, since a Go
// build's kernel artifact stat differs from the TS build's).
package kernelbridge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

const moduleAbortedMarker = "the module aborted on this question"

// canonBudgetChars mirrors CANON_BUDGET_CHARS in the TS source: the
// spelling budget counts OUTPUT LENGTH, not visits — a shared sub-DAG's
// spelling is inlined at every reference, so a bounded number of nodes
// can still formatAt exponentially many characters — the grammar-scale
// sets do exactly that, and they simply exceed the budget and keep
// their compact wire keys.
const canonBudgetChars = 16384

var errCanonBudgetExhausted = fmt.Errorf("canon budget")

// canonicalKeyWithBudget mirrors the TS function of the same name. It
// walks the wire-shaped value (as produced by wireSet/wireForm/etc — a
// tree of map[string]any / []any / primitives, the Go analogue of the
// TS `unknown` JSON value) and spells a canonical, order-free key.
func canonicalKeyWithBudget(v any, budget *int) (string, error) {
	spend := func(piece string) (string, error) {
		*budget -= len(piece)
		if *budget <= 0 {
			return "", errCanonBudgetExhausted
		}
		return piece, nil
	}
	if v == nil {
		return spend("null")
	}
	switch value := v.(type) {
	case []any:
		parts := make([]string, len(value))
		for i, x := range value {
			piece, err := canonicalKeyWithBudget(x, budget)
			if err != nil {
				return "", err
			}
			parts[i] = piece
		}
		return spend("[" + joinComma(parts) + "]")
	case map[string]any:
		if rawForms, held := value["forms"]; held {
			if forms, ok := rawForms.([]any); ok {
				// an intersection: order-free, so the keys sort
				spelled := make([]string, len(forms))
				for i, f := range forms {
					piece, err := canonicalKeyWithBudget(f, budget)
					if err != nil {
						return "", err
					}
					spelled[i] = piece
				}
				sort.Strings(spelled)
				return spend("{forms:[" + joinComma(spelled) + "]}")
			}
		}
		if value["form"] == "union" {
			// a union commutes: the branches sort by key
			a, err := canonicalKeyWithBudget(value["A"], budget)
			if err != nil {
				return "", err
			}
			b, err := canonicalKeyWithBudget(value["B"], budget)
			if err != nil {
				return "", err
			}
			if b < a {
				a, b = b, a
			}
			return spend(fmt.Sprintf(`{form:"union",A:%s,B:%s}`, a, b))
		}
		keys := make([]string, 0, len(value))
		for k := range value {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			piece, err := canonicalKeyWithBudget(value[k], budget)
			if err != nil {
				return "", err
			}
			parts[i] = fmt.Sprintf("%s:%s", k, piece)
		}
		return spend("{" + joinComma(parts) + "}")
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		return spend(string(encoded))
	}
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}

// The TS canonicalKeyOf memoizes per input object in a
// `WeakMap<object, string | null>`, keyed on the CALLER's own
// RefinedSet/Chain/etc identity — an object read once and asked about
// many times in a walk hits the memo on every ask after the first.
//
// This Go port has no such memo. Two obstacles compound: (1) every
// call site here passes `wireSet(set)` — a map/slice tree BUILT FRESH
// per call — so even a correct WeakMap analogue would never see the
// same key object twice, and (2) map[string]any/[]any are not
// comparable in Go, so they cannot key a map at all (a lookup panics:
// "hash of unhashable type"). A real fix would key the memo on the
// ORIGINAL RefinedSet's pointer at every call site instead of its
// wireSet() value — a larger restructuring than this file alone
// covers, since every caller in kernel_asks.go / loop_questions.go /
// etc. would need to pass the RefinedSet (or a pointer to it) through
// to CanonicalKeyOf rather than its wire form. Dropped here; every
// call recomputes the canonical spelling from scratch. Correctness is
// unaffected (the memo was purely a speed optimization), but the speed
// win it names in TS (canonicalKey.memoHit) does not exist in this
// port. Reported as a gap.

// CanonicalPair is canonicalPair in the TS source.
func CanonicalPair(a *string, b *string) (string, bool) {
	if a == nil || b == nil {
		return "", false
	}
	return *a + "" + *b, true
}

// CanonicalKeyOf is canonicalKeyOf in the TS source, minus the memo —
// see the file comment just above. Returns nil when the canonical
// spelling exceeded the budget — the caller keeps its compact wire key
// instead.
func CanonicalKeyOf(value any) *string {
	startedAt := tracing.Clock()
	budget := canonBudgetChars
	key, err := canonicalKeyWithBudget(value, &budget)
	tracing.Count("canonicalKey", tracing.Clock()-startedAt)
	if err != nil {
		return nil
	}
	return &key
}

// CanonicalKeyOfSet is the order-free canonical key of one refined
// set — canonicalKeyOf(wireSet(set)) in the TS source, exported so
// set-keyed memos outside this package (annotations' interface hash)
// key on the canonical spelling rather than the order-sensitive wire
// string. Nil past the spelling budget, like CanonicalKeyOf.
func CanonicalKeyOfSet(set refinementsets.RefinedSet) *string {
	return CanonicalKeyOf(wireSet(set))
}

// QuestionCacheEntry is the TS `{ ok: string } | { err: string }`.
type QuestionCacheEntry struct {
	Ok      string
	Err     string
	IsError bool
}

// questionCacheCapacity mirrors QUESTION_CACHE_CAPACITY (= ANSWERS_KEPT
// in service/cache_tuning.ts, ported inline here since service/ has no
// Go twin yet — see the port report).
const questionCacheCapacity = 16384

// questionCache is an ordered map (insertion order = LRU order,
// refreshed on hit) mirroring the TS `Map`'s iteration-order guarantee.
type questionCacheStore struct {
	order   []string
	entries map[string]QuestionCacheEntry
}

func newQuestionCacheStore() *questionCacheStore {
	return &questionCacheStore{entries: map[string]QuestionCacheEntry{}}
}

func (s *questionCacheStore) get(key string) (QuestionCacheEntry, bool) {
	e, ok := s.entries[key]
	return e, ok
}

func (s *questionCacheStore) delete(key string) {
	if _, ok := s.entries[key]; !ok {
		return
	}
	delete(s.entries, key)
	for i, k := range s.order {
		if k == key {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
}

func (s *questionCacheStore) set(key string, entry QuestionCacheEntry) {
	if _, held := s.entries[key]; !held {
		s.order = append(s.order, key)
	}
	s.entries[key] = entry
}

func (s *questionCacheStore) size() int { return len(s.order) }

func (s *questionCacheStore) oldestKey() (string, bool) {
	if len(s.order) == 0 {
		return "", false
	}
	return s.order[0], true
}

var questionCache = newQuestionCacheStore()
var dirtyAnswers = 0
var storeLoaded = false

// KernelArtifactPathFunc is set by kernel_bridge.go — a forward
// reference so this file, ported from question_cache.ts, can call
// kernelArtifactPath() without an import cycle (both files sit in the
// same package, so this indirection is only to keep each file's
// contents 1:1 with its TS ordering; a plain function reference set at
// package init would also do, but this mirrors "loaded lazily" most
// directly).
var kernelArtifactPathFn func() string

func storeSalt() string {
	if kernelArtifactPathFn == nil {
		return "no-kernel"
	}
	// the ACTIVE artifact so a rebuilt kernel never serves another
	// build's stored answers
	info, err := os.Stat(kernelArtifactPathFn())
	if err != nil {
		return "no-kernel"
	}
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixMilli())
}

func storePath() string {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}
	return filepath.Join(home, ".cache", "refinedts", "questions-go-v1.json")
}

// Only compact entries persist: an oversized key (a grammar-scale
// wire) stays in the in-process cache but never on disk, and an
// implausibly large store file is refused outright rather than parsed
// into memory.
const storeEntryLimitBytes = 4096
const storeFileLimitBytes = 32 * 1024 * 1024

type storedFile struct {
	Salt    string               `json:"salt"`
	Entries [][2]json.RawMessage `json:"entries"`
}

func loadQuestionStore() {
	if storeLoaded {
		return
	}
	storeLoaded = true
	info, err := os.Stat(storePath())
	if err != nil {
		return // no store yet
	}
	if info.Size() > storeFileLimitBytes {
		return
	}
	raw, err := os.ReadFile(storePath())
	if err != nil {
		return
	}
	var parsed storedFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return
	}
	if parsed.Salt != storeSalt() {
		return
	}
	for _, pair := range parsed.Entries {
		if questionCache.size() >= questionCacheCapacity {
			break
		}
		var key string
		if err := json.Unmarshal(pair[0], &key); err != nil {
			continue
		}
		if _, held := questionCache.get(key); held {
			continue
		}
		var raw map[string]string
		if err := json.Unmarshal(pair[1], &raw); err != nil {
			continue
		}
		if ok, held := raw["ok"]; held {
			questionCache.set(key, QuestionCacheEntry{Ok: ok})
		} else if errText, held := raw["err"]; held {
			questionCache.set(key, QuestionCacheEntry{Err: errText, IsError: true})
		}
	}
}

// FlushQuestionStore is flushQuestionStore in the TS source: write
// newly earned answers to disk. Called at the end of a check; a run
// with nothing new writes nothing.
func FlushQuestionStore() {
	if dirtyAnswers == 0 {
		return
	}
	dirtyAnswers = 0
	dir := filepath.Dir(storePath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return // a read-only environment checks fine without the store
	}
	// only ANSWERS persist — an error is a fact about one run (a
	// refused question, a wire bug, an out-of-memory prover), and
	// replaying it from disk poisons every later run. The kernel
	// reports errors INSIDE a successful response, so the body is
	// inspected, not just the transport.
	var compact [][2]any
	for _, key := range questionCache.order {
		entry := questionCache.entries[key]
		if entry.IsError {
			continue
		}
		if len(entry.Ok) >= 8 && entry.Ok[:8] == `{"error"` {
			continue
		}
		if len(key)+len(entry.Ok) > storeEntryLimitBytes {
			continue
		}
		compact = append(compact, [2]any{key, map[string]string{"ok": entry.Ok}})
	}
	out, err := json.Marshal(map[string]any{"salt": storeSalt(), "entries": compact})
	if err != nil {
		return
	}
	_ = os.WriteFile(storePath(), out, 0o644)
}

// ClearQuestionCache is clearQuestionCache in the TS source: bench
// seam — empty the question cache, so a profile can measure the NOVEL-
// question regime (a file's first check) instead of the recheck regime
// where every answer is a hit. Never called by the checker itself.
func ClearQuestionCache() {
	questionCache = newQuestionCacheStore()
}

// AskCached is askCached in the TS source.
func AskCached(key string, compute func() (string, error)) (string, error) {
	loadQuestionStore()
	if held, ok := questionCache.get(key); ok {
		tracing.Count("kernel.cacheHit", 0)
		TraceQuestionLine(fmt.Sprintf("kernel cachehit %s", firstLine(key)))
		lookedUpAt := tracing.Clock()
		questionCache.delete(key)
		questionCache.set(key, held) // LRU refresh
		tracing.Count("kernel.cacheLookup", tracing.Clock()-lookedUpAt)
		if held.IsError {
			return "", fmt.Errorf("%s", held.Err)
		}
		return held.Ok, nil
	}
	type computed struct {
		ok  string
		err error
	}
	answer := tracing.Span("kernel.ask", func() computed {
		ok, err := compute()
		return computed{ok: ok, err: err}
	}, tracing.GrainStep)
	ok, err := answer.ok, answer.err
	if err != nil {
		// A decline is a deterministic property of the question and is
		// worth remembering — but a module that ABORTED is an operational
		// failure, not a property of anything. Caching that would make one
		// out-of-memory answer permanent, and the store persists to disk,
		// so it would outlive the process that hit it.
		if !strings.Contains(err.Error(), moduleAbortedMarker) {
			remember(key, QuestionCacheEntry{Err: err.Error(), IsError: true})
			dirtyAnswers += 1
		}
		return "", err
	}
	remember(key, QuestionCacheEntry{Ok: ok})
	dirtyAnswers += 1
	return ok, nil
}

func remember(key string, entry QuestionCacheEntry) {
	if questionCache.size() >= questionCacheCapacity {
		if oldest, held := questionCache.oldestKey(); held {
			questionCache.delete(oldest)
		}
	}
	questionCache.set(key, entry)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i != -1 {
		return s[:i]
	}
	return s
}
