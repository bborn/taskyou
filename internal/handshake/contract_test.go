package handshake_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
	"github.com/bborn/workflow/internal/handshake"
	"github.com/bborn/workflow/internal/tmuxctl"
)

// contractInputs are the things a daemon and a client must agree on. They live
// in three packages; this is the one place that names all of them together.
//
// It is deliberately a list of values, not a list of package names: a rename or
// a refactor that does not change what is on the wire leaves the fingerprint
// alone, and only a real change to the contract moves it.
func contractInputs() []string {
	in := []string{
		fmt.Sprintf("db.SchemaVersion=%d", db.SchemaVersion),
		"executor.ClaudeHookEvents=" + strings.Join(executor.ClaudeHookEvents, ","),
		"tmuxctl.PrivateSocket=" + tmuxctl.PrivateSocket,
		"tmuxctl.EnvSocket=" + tmuxctl.EnvSocket,
		"tmuxctl.PaneTaskOption=" + tmuxctl.PaneTaskOption,
		"tmuxctl.PaneRoleOption=" + tmuxctl.PaneRoleOption,
		"tmuxctl.RoleAgent=" + tmuxctl.RoleAgent,
		"tmuxctl.RoleShell=" + tmuxctl.RoleShell,
		"executor.TmuxWindowName=" + executor.TmuxWindowName(42),
	}
	return in
}

func fingerprint() string {
	sum := sha256.Sum256([]byte(strings.Join(contractInputs(), "\n")))
	return hex.EncodeToString(sum[:])[:16]
}

// TestContractFingerprint is the forcing function behind handshake.Protocol.
//
// Protocol cannot be derived automatically — a hash makes a useless error
// message and moves on harmless refactors — so it is a hand-written integer
// with this test standing behind it. Change the schema version, the hook event
// set or the tmux conventions and this fails, telling you to bump Protocol and
// paste in the new fingerprint. Two deliberate edits, which is the point.
func TestContractFingerprint(t *testing.T) {
	got := fingerprint()
	if got != handshake.ContractFingerprint {
		t.Fatalf(`the daemon↔client contract changed.

The contract inputs now fingerprint as %q, but internal/handshake pins %q.

If the change means a daemon and a client of different builds would now
disagree with each other, bump handshake.Protocol by one. Either way, set:

    const ContractFingerprint = %q

Current inputs:
  %s`, got, handshake.ContractFingerprint, got, strings.Join(contractInputs(), "\n  "))
	}
}

// TestProtocolIsPositive guards against a Protocol of 0, which Compare cannot
// distinguish from a record written by something that did not set the field.
func TestProtocolIsPositive(t *testing.T) {
	if handshake.Protocol < 1 {
		t.Fatalf("Protocol = %d, must be >= 1", handshake.Protocol)
	}
}
