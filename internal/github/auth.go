package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// AccountType classifies the identity that `gh` is authenticated as.
type AccountType string

const (
	// AccountTypeUnknown means we could not determine the identity.
	AccountTypeUnknown AccountType = "unknown"
	// AccountTypePersonal is a regular GitHub user account (e.g. via `gh auth login`).
	// Personal accounts share a single 5,000 pt/hr GraphQL bucket PER-USER across
	// every machine authenticated as that user — the root cause of bucket exhaustion.
	AccountTypePersonal AccountType = "personal"
	// AccountTypeApp is a GitHub App / bot identity (login ends in "[bot]",
	// API type "Bot"). Each App installation token gets its OWN GraphQL bucket,
	// so it does not contend with other servers.
	AccountTypeApp AccountType = "app"
)

// Severity ranks a diagnostic finding.
type Severity string

const (
	SeverityOK    Severity = "ok"
	SeverityWarn  Severity = "warn"
	SeverityError Severity = "error"
)

// Finding is a single diagnostic result from the GitHub auth check.
type Finding struct {
	Severity Severity
	Message  string
	Detail   string // optional remediation hint
}

// AuthStatus is the result of inspecting the local `gh` authentication.
type AuthStatus struct {
	GHInstalled bool
	LoggedIn    bool
	TokenValid  bool // false when the token is expired/revoked (401 Bad credentials)

	Account     string
	AccountType AccountType

	// GraphQL rate-limit headroom. Remaining/Limit are -1 when unknown.
	GraphQLRemaining int
	GraphQLLimit     int
	GraphQLResetAt   time.Time

	// Err holds an unexpected error encountered while probing (not auth state).
	Err error
}

// ghUserResponse is the subset of `gh api user` we care about.
type ghUserResponse struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

// rateLimitResponse mirrors `gh api rate_limit`.
type rateLimitResponse struct {
	Resources struct {
		GraphQL struct {
			Limit     int   `json:"limit"`
			Remaining int   `json:"remaining"`
			Reset     int64 `json:"reset"`
		} `json:"graphql"`
	} `json:"resources"`
}

// CheckAuth inspects the local `gh` CLI authentication and rate-limit headroom.
// It never returns an error for ordinary auth states (not installed, logged out,
// expired token); those are reported via the returned AuthStatus fields.
func CheckAuth(ctx context.Context) AuthStatus {
	status := AuthStatus{
		GraphQLRemaining: -1,
		GraphQLLimit:     -1,
	}

	if _, err := exec.LookPath("gh"); err != nil {
		return status
	}
	status.GHInstalled = true

	// Probe the authenticated identity via the REST user endpoint. This both
	// confirms the token is valid and tells us whether it's a personal account
	// or a bot/App identity.
	userCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(userCtx, "gh", "api", "user", "--jq", "{login: .login, type: .type}")
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = string(exitErr.Stderr)
		}
		st := classifyUserErr(stderr)
		status.LoggedIn = st.loggedIn
		status.TokenValid = st.tokenValid
		if st.unknown {
			// Unknown failure (network, timeout). Surface it but don't crash.
			status.Err = fmt.Errorf("gh api user failed: %w", err)
		}
		return status
	}

	status.LoggedIn = true
	status.TokenValid = true

	var user ghUserResponse
	if jsonErr := json.Unmarshal(out, &user); jsonErr == nil {
		status.Account = user.Login
		status.AccountType = classifyAccount(user.Login, user.Type)
	}

	// Best-effort GraphQL headroom probe. Failures leave the -1 sentinels.
	rl := fetchRateLimit(ctx)
	if rl != nil {
		status.GraphQLRemaining = rl.Resources.GraphQL.Remaining
		status.GraphQLLimit = rl.Resources.GraphQL.Limit
		if rl.Resources.GraphQL.Reset > 0 {
			status.GraphQLResetAt = time.Unix(rl.Resources.GraphQL.Reset, 0)
		}
	}

	return status
}

// fetchRateLimit queries the GraphQL rate-limit bucket. Returns nil on failure.
func fetchRateLimit(ctx context.Context) *rateLimitResponse {
	rlCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(rlCtx, "gh", "api", "rate_limit").Output()
	if err != nil {
		return nil
	}
	var rl rateLimitResponse
	if err := json.Unmarshal(out, &rl); err != nil {
		return nil
	}
	return &rl
}

// classifyAccount decides whether an identity is a personal user or a bot/App.
// GitHub App installation tokens authenticate as a Bot user whose login ends in
// "[bot]" and whose API type is "Bot".
func classifyAccount(login, apiType string) AccountType {
	if strings.EqualFold(apiType, "Bot") || strings.HasSuffix(login, "[bot]") {
		return AccountTypeApp
	}
	if login != "" {
		return AccountTypePersonal
	}
	return AccountTypeUnknown
}

// userErrState is the auth state inferred from a FAILED `gh api user` call.
//
//	stderr pattern         → state
//	─────────────────────────────────────────────
//	"Bad credentials"/401  → loggedIn, !tokenValid  (token expired/revoked)
//	"not logged in"/…      → !loggedIn               (no auth configured)
//	anything else          → unknown                 (network/timeout — caller sets Err)
type userErrState struct {
	loggedIn   bool
	tokenValid bool
	unknown    bool // stderr matched neither known pattern
}

// classifyUserErr maps `gh api user` stderr to an auth state. Pure and table-tested
// so a future change to gh's wording is caught rather than silently mis-routed.
func classifyUserErr(stderr string) userErrState {
	switch {
	case isBadCredentials(stderr):
		// Config present (logged in) but the token is expired/revoked.
		return userErrState{loggedIn: true, tokenValid: false}
	case isNotLoggedIn(stderr):
		return userErrState{loggedIn: false}
	default:
		return userErrState{unknown: true}
	}
}

func isBadCredentials(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "bad credentials") ||
		strings.Contains(s, "401") ||
		strings.Contains(s, "requires authentication")
}

func isNotLoggedIn(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "not logged") ||
		strings.Contains(s, "no accounts") ||
		strings.Contains(s, "authentication required") ||
		strings.Contains(s, "to get started with github cli") ||
		strings.Contains(s, "gh auth login")
}

// graphQLLowThreshold is the remaining-points level below which `ty doctor` warns
// the OPERATOR that the bucket is close to exhaustion.
//
// This is intentionally higher than rateLimitThreshold (200) in pr.go, which gates
// automatic batch PR fetches. The two serve different jobs and should not be merged:
//   - rateLimitThreshold (200): the TUI's own self-throttle — stop spending budget.
//   - graphQLLowThreshold (500): warn a human earlier, before automatic throttling
//     kicks in, so they can act (e.g. re-provision a bot token) with headroom to spare.
const graphQLLowThreshold = 500

// Findings translates the auth status into ordered diagnostic findings. The most
// severe issues come first. This is the data `ty doctor` renders.
func (s AuthStatus) Findings() []Finding {
	var findings []Finding

	if !s.GHInstalled {
		return []Finding{{
			Severity: SeverityError,
			Message:  "GitHub CLI (gh) is not installed",
			Detail:   "Install gh so agents can interact with GitHub: https://cli.github.com",
		}}
	}

	if !s.LoggedIn {
		return []Finding{{
			Severity: SeverityError,
			Message:  "gh is not logged in to any GitHub account",
			Detail:   "GitHub operations (gh pr ...) will silently fail. Authenticate this server with its own GitHub App installation token.",
		}}
	}

	if !s.TokenValid {
		return []Finding{{
			Severity: SeverityError,
			Message:  "gh token is expired or revoked (401 Bad credentials)",
			Detail:   "GitHub operations are silently failing. Re-provision this server's GitHub App installation token.",
		}}
	}

	switch s.AccountType {
	case AccountTypeApp:
		findings = append(findings, Finding{
			Severity: SeverityOK,
			Message:  fmt.Sprintf("Authenticated as GitHub App / bot identity %q", s.Account),
			Detail:   "Bot identities get their own GraphQL bucket — no cross-server contention.",
		})
	case AccountTypePersonal:
		findings = append(findings, Finding{
			Severity: SeverityWarn,
			Message:  fmt.Sprintf("Authenticated as personal account %q", s.Account),
			Detail:   "GitHub's GraphQL limit is PER-USER. Every server authenticated as this same account shares ONE 5,000 pt/hr bucket and will exhaust it under load. Provision this server with its own GitHub App installation token (bot identity) instead.",
		})
	default:
		findings = append(findings, Finding{
			Severity: SeverityWarn,
			Message:  "Could not determine the GitHub account type",
			Detail:   "Run `gh api user` to inspect the authenticated identity.",
		})
	}

	// GraphQL headroom.
	if s.GraphQLRemaining >= 0 && s.GraphQLLimit > 0 {
		msg := fmt.Sprintf("GraphQL bucket: %d/%d points remaining", s.GraphQLRemaining, s.GraphQLLimit)
		if !s.GraphQLResetAt.IsZero() {
			msg += fmt.Sprintf(" (resets %s)", s.GraphQLResetAt.Format(time.Kitchen))
		}
		sev := SeverityOK
		detail := ""
		if s.GraphQLRemaining < graphQLLowThreshold {
			sev = SeverityWarn
			detail = "Bucket is nearly exhausted. Prefer REST for PR reads and avoid `gh pr checks` polling loops until it resets."
		}
		findings = append(findings, Finding{Severity: sev, Message: msg, Detail: detail})
	}

	return findings
}

// HasProblems reports whether any finding is a warning or error.
func (s AuthStatus) HasProblems() bool {
	for _, f := range s.Findings() {
		if f.Severity != SeverityOK {
			return true
		}
	}
	return false
}
