package github

import (
	"testing"
	"time"
)

func TestClassifyAccount(t *testing.T) {
	tests := []struct {
		name    string
		login   string
		apiType string
		want    AccountType
	}{
		{"bot by type", "offerlab-agents[bot]", "Bot", AccountTypeApp},
		{"bot by login suffix only", "ci-runner[bot]", "", AccountTypeApp},
		{"bot type case-insensitive", "x", "bot", AccountTypeApp},
		{"personal user", "bborn", "User", AccountTypePersonal},
		{"personal no type", "someone", "", AccountTypePersonal},
		{"unknown empty", "", "", AccountTypeUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyAccount(tt.login, tt.apiType); got != tt.want {
				t.Errorf("classifyAccount(%q, %q) = %q, want %q", tt.login, tt.apiType, got, tt.want)
			}
		})
	}
}

func TestIsBadCredentials(t *testing.T) {
	yes := []string{
		"HTTP 401: Bad credentials (https://api.github.com/user)",
		"gh: Bad credentials",
		"This endpoint requires authentication",
		"error: 401",
	}
	for _, s := range yes {
		if !isBadCredentials(s) {
			t.Errorf("isBadCredentials(%q) = false, want true", s)
		}
	}
	no := []string{"", "some network error", "rate limit exceeded"}
	for _, s := range no {
		if isBadCredentials(s) {
			t.Errorf("isBadCredentials(%q) = true, want false", s)
		}
	}
}

func TestIsNotLoggedIn(t *testing.T) {
	yes := []string{
		"You are not logged into any GitHub hosts. To get started with GitHub CLI, please run: gh auth login",
		"no accounts found",
		"authentication required",
	}
	for _, s := range yes {
		if !isNotLoggedIn(s) {
			t.Errorf("isNotLoggedIn(%q) = false, want true", s)
		}
	}
	if isNotLoggedIn("Bad credentials") {
		// Bad credentials means logged in but expired, not logged out.
		// (isBadCredentials handles that case separately.)
	}
}

func TestFindings_NotInstalled(t *testing.T) {
	s := AuthStatus{GHInstalled: false}
	findings := s.Findings()
	if len(findings) != 1 || findings[0].Severity != SeverityError {
		t.Fatalf("expected single error finding, got %+v", findings)
	}
	if !s.HasProblems() {
		t.Error("HasProblems() = false, want true")
	}
}

func TestFindings_LoggedOut(t *testing.T) {
	s := AuthStatus{GHInstalled: true, LoggedIn: false}
	findings := s.Findings()
	if len(findings) != 1 || findings[0].Severity != SeverityError {
		t.Fatalf("expected single error finding, got %+v", findings)
	}
}

func TestFindings_ExpiredToken(t *testing.T) {
	s := AuthStatus{GHInstalled: true, LoggedIn: true, TokenValid: false}
	findings := s.Findings()
	if len(findings) != 1 || findings[0].Severity != SeverityError {
		t.Fatalf("expected single error finding for expired token, got %+v", findings)
	}
}

func TestFindings_PersonalAccountWarns(t *testing.T) {
	s := AuthStatus{
		GHInstalled:      true,
		LoggedIn:         true,
		TokenValid:       true,
		Account:          "bborn",
		AccountType:      AccountTypePersonal,
		GraphQLRemaining: 4800,
		GraphQLLimit:     5000,
	}
	findings := s.Findings()
	if len(findings) < 1 {
		t.Fatal("expected at least one finding")
	}
	if findings[0].Severity != SeverityWarn {
		t.Errorf("personal account finding severity = %q, want warn", findings[0].Severity)
	}
	if !s.HasProblems() {
		t.Error("HasProblems() = false, want true for personal account")
	}
}

func TestFindings_AppAccountHealthy(t *testing.T) {
	s := AuthStatus{
		GHInstalled:      true,
		LoggedIn:         true,
		TokenValid:       true,
		Account:          "offerlab-agents[bot]",
		AccountType:      AccountTypeApp,
		GraphQLRemaining: 5000,
		GraphQLLimit:     5000,
		GraphQLResetAt:   time.Now().Add(time.Hour),
	}
	findings := s.Findings()
	for _, f := range findings {
		if f.Severity != SeverityOK {
			t.Errorf("expected all OK findings for healthy bot, got %q: %s", f.Severity, f.Message)
		}
	}
	if s.HasProblems() {
		t.Error("HasProblems() = true, want false for healthy bot identity")
	}
}

func TestFindings_LowGraphQLHeadroomWarns(t *testing.T) {
	s := AuthStatus{
		GHInstalled:      true,
		LoggedIn:         true,
		TokenValid:       true,
		Account:          "offerlab-agents[bot]",
		AccountType:      AccountTypeApp,
		GraphQLRemaining: 100, // below threshold
		GraphQLLimit:     5000,
	}
	var sawWarn bool
	for _, f := range s.Findings() {
		if f.Severity == SeverityWarn {
			sawWarn = true
		}
	}
	if !sawWarn {
		t.Error("expected a warning for low GraphQL headroom")
	}
}
