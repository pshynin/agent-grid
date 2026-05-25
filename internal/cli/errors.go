package cli

import "errors"

// PolicyError signals a refusal driven by policy (overlap conflict, risk
// override required, etc.). The main entry point converts this into exit
// code 3.
type PolicyError struct{ Msg string }

func (e *PolicyError) Error() string { return e.Msg }

// ExitCode maps a top-level error from Execute() to a process exit code.
//
//   - nil               -> 0
//   - *PolicyError      -> 3 (policy refusal)
//   - any other error   -> 1 (user / system error)
//
// Exit code 2 (system error) is reserved for a future split between user and
// system errors; not used in v0.1.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var pe *PolicyError
	if errors.As(err, &pe) {
		return 3
	}
	return 1
}
