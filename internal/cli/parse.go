package cli

import (
	"fmt"
	"strings"

	"github.com/pshynin/agent-grid/internal/core"
)

// ParseClaim parses a string of the form "<kind>:<pattern>:<intent>" into its
// three components. Whitespace around each component is trimmed. The pattern
// may itself contain colons because the parser splits at the first and last
// colon only.
func ParseClaim(s string) (core.ClaimKind, string, core.ClaimIntent, error) {
	first := strings.IndexByte(s, ':')
	last := strings.LastIndexByte(s, ':')
	if first < 0 || first == last {
		return "", "", "", fmt.Errorf(`claim %q: expected "<kind>:<pattern>:<intent>"`, s)
	}
	kind := strings.TrimSpace(s[:first])
	pattern := strings.TrimSpace(s[first+1 : last])
	intent := strings.TrimSpace(s[last+1:])
	if kind == "" || pattern == "" || intent == "" {
		return "", "", "", fmt.Errorf(`claim %q: empty field in "<kind>:<pattern>:<intent>"`, s)
	}
	return core.ClaimKind(kind), pattern, core.ClaimIntent(intent), nil
}
