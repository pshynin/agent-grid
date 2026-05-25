// Package policy holds pure decision functions for AgentGrid: claim overlap,
// stale detection, and diff-risk scoring. Nothing in this package performs
// I/O.
package policy

import (
	"fmt"
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/pshynin/agent-grid/internal/core"
)

// OverlapVerdict is the result of checking a new claim against existing claims.
type OverlapVerdict struct {
	Conflicts []OverlapConflict
}

// OverlapConflict describes a single existing claim that conflicts with the
// new one.
type OverlapConflict struct {
	With       core.Claim // the existing claim
	NewPattern string     // the pattern of the new claim
}

// HasConflict reports whether the verdict contains any conflicts.
func (v OverlapVerdict) HasConflict() bool { return len(v.Conflicts) > 0 }

// CheckOverlap reports conflicts between a new claim and a set of existing
// claims. Existing claims belonging to the same agent are ignored: an agent
// may layer multiple claims of its own.
//
// Conflict rules (MVP):
//   - read+read never conflicts.
//   - any other intent pairing conflicts when the patterns overlap.
//
// Pattern overlap rules (MVP):
//   - path vs path: literal equality after cleaning.
//   - path vs glob (either direction): the glob matches the literal path.
//   - glob vs glob: equality, or a witness path exists that both patterns
//     match. The witness search is heuristic; it covers the common cases
//     (shared prefixes, nested directories, prefix+suffix combinations) but
//     is not a complete pattern intersection.
func CheckOverlap(newClaim core.Claim, existing []core.Claim) (OverlapVerdict, error) {
	if err := ValidateClaim(newClaim); err != nil {
		return OverlapVerdict{}, fmt.Errorf("new claim: %w", err)
	}
	var v OverlapVerdict
	for _, e := range existing {
		if e.AgentID == newClaim.AgentID {
			continue
		}
		if err := ValidateClaim(e); err != nil {
			return OverlapVerdict{}, fmt.Errorf("existing claim %q: %w", e.ID, err)
		}
		if !intentsConflict(newClaim.Intent, e.Intent) {
			continue
		}
		if !patternsOverlap(newClaim, e) {
			continue
		}
		v.Conflicts = append(v.Conflicts, OverlapConflict{
			With:       e,
			NewPattern: newClaim.Pattern,
		})
	}
	return v, nil
}

// ValidateClaim checks that a claim's kind, intent, and pattern are valid for
// the MVP. The MVP supports only kinds {path, glob} and intents {edit, read}.
func ValidateClaim(c core.Claim) error {
	switch c.Kind {
	case core.ClaimKindPath, core.ClaimKindGlob:
		// ok
	case "":
		return fmt.Errorf("kind: missing (must be %q or %q)",
			core.ClaimKindPath, core.ClaimKindGlob)
	default:
		return fmt.Errorf("kind: %q is not supported in v0.1 (must be %q or %q)",
			c.Kind, core.ClaimKindPath, core.ClaimKindGlob)
	}

	switch c.Intent {
	case core.ClaimIntentEdit, core.ClaimIntentRead:
		// ok
	case "":
		return fmt.Errorf("intent: missing (must be %q or %q)",
			core.ClaimIntentEdit, core.ClaimIntentRead)
	default:
		return fmt.Errorf("intent: %q is not supported in v0.1 (must be %q or %q)",
			c.Intent, core.ClaimIntentEdit, core.ClaimIntentRead)
	}

	if strings.TrimSpace(c.Pattern) == "" {
		return fmt.Errorf("pattern: must be non-empty")
	}

	if c.Kind == core.ClaimKindGlob {
		if !doublestar.ValidatePattern(c.Pattern) {
			return fmt.Errorf("pattern: %q is not a valid doublestar glob", c.Pattern)
		}
	}
	return nil
}

func intentsConflict(a, b core.ClaimIntent) bool {
	// read+read is the only allowed pairing.
	return !(a == core.ClaimIntentRead && b == core.ClaimIntentRead)
}

func patternsOverlap(a, b core.Claim) bool {
	aPat, aIsGlob := normalize(a)
	bPat, bIsGlob := normalize(b)

	switch {
	case !aIsGlob && !bIsGlob:
		return aPat == bPat
	case !aIsGlob && bIsGlob:
		return globMatch(bPat, aPat)
	case aIsGlob && !bIsGlob:
		return globMatch(aPat, bPat)
	default:
		return globsIntersect(aPat, bPat)
	}
}

func normalize(c core.Claim) (string, bool) {
	p := strings.TrimSpace(c.Pattern)
	if c.Kind == core.ClaimKindPath {
		return path.Clean(p), false
	}
	// For glob: trim a single trailing slash; doublestar treats `a/**/` and
	// `a/**` identically in practice but trimming keeps witnesses tidy.
	p = strings.TrimRight(p, "/")
	if p == "" {
		p = "/"
	}
	return p, true
}

func globMatch(pattern, name string) bool {
	ok, err := doublestar.Match(pattern, name)
	return err == nil && ok
}

// globsIntersect returns true if there is plausibly a path matched by both
// glob patterns. The strategy is heuristic:
//
//  1. String equality.
//  2. Simple witness from each pattern (wildcards replaced by "x"); check the
//     other pattern matches it.
//  3. Hybrid witness using the literal prefix of one pattern and the literal
//     suffix of the other; check both patterns match it.
//
// This covers the realistic MVP cases (shared prefix, nested directories,
// prefix+suffix combinations like "pkg/**" vs "**/*.go"). It is not a
// complete pattern intersection algorithm.
func globsIntersect(a, b string) bool {
	if a == b {
		return true
	}
	if w := simpleWitness(a); w != "" && globMatch(b, w) {
		return true
	}
	if w := simpleWitness(b); w != "" && globMatch(a, w) {
		return true
	}
	if w := hybridWitness(a, b); w != "" && globMatch(a, w) && globMatch(b, w) {
		return true
	}
	if w := hybridWitness(b, a); w != "" && globMatch(a, w) && globMatch(b, w) {
		return true
	}
	return false
}

// simpleWitness returns a concrete path that the pattern matches by
// replacing wildcards with "x". Character classes resolve to their first
// member; brace alternations resolve to their first alternative. Best
// effort; returns the original string for fully literal patterns.
func simpleWitness(pattern string) string {
	var b strings.Builder
	i := 0
	for i < len(pattern) {
		switch {
		case strings.HasPrefix(pattern[i:], "**"):
			b.WriteByte('x')
			i += 2
		case pattern[i] == '*':
			b.WriteByte('x')
			i++
		case pattern[i] == '?':
			b.WriteByte('x')
			i++
		case pattern[i] == '[':
			j := strings.IndexByte(pattern[i:], ']')
			if j < 0 {
				b.WriteByte(pattern[i])
				i++
				continue
			}
			class := strings.TrimPrefix(pattern[i+1:i+j], "!")
			if len(class) > 0 {
				b.WriteByte(class[0])
			} else {
				b.WriteByte('x')
			}
			i += j + 1
		case pattern[i] == '{':
			j := strings.IndexByte(pattern[i:], '}')
			if j < 0 {
				b.WriteByte(pattern[i])
				i++
				continue
			}
			alts := strings.Split(pattern[i+1:i+j], ",")
			if len(alts) > 0 && alts[0] != "" {
				b.WriteString(alts[0])
			}
			i += j + 1
		default:
			b.WriteByte(pattern[i])
			i++
		}
	}
	return b.String()
}

// hybridWitness builds a candidate path from the literal prefix of one
// pattern and the literal suffix of another. Returns "" if neither pattern
// has both a usable prefix and the other has a usable suffix.
func hybridWitness(prefixOf, suffixOf string) string {
	pre := literalPrefix(prefixOf)
	suf := literalSuffix(suffixOf)
	if pre == prefixOf && suf == suffixOf {
		// Both fully literal — would have hit the equality case already.
		return ""
	}
	if pre == "" && suf == "" {
		return ""
	}
	bridge := "x"
	if pre == "" || suf == "" {
		bridge = "x"
	} else if strings.HasSuffix(pre, "/") || strings.HasPrefix(suf, "/") {
		bridge = "x"
	}
	return pre + bridge + suf
}

func literalPrefix(pattern string) string {
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*', '?', '[', '{':
			return pattern[:i]
		}
	}
	return pattern
}

func literalSuffix(pattern string) string {
	for i := len(pattern) - 1; i >= 0; i-- {
		switch pattern[i] {
		case '*', '?', ']', '}':
			return pattern[i+1:]
		}
	}
	return pattern
}
