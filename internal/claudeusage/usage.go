// Package claudeusage reads how much of a Claude subscription's rate limits a
// given Claude Code profile has burned through.
//
// A "profile" here is a CLAUDE_CONFIG_DIR: one logged-in Claude account with
// its own credentials. Someone with two accounts (say a personal one and a work
// one) keeps them in two config dirs and points ty at whichever they want a task
// to run under. This package answers the question that makes *choosing* between
// them possible — "how much headroom does each one have left?" — by reading the
// profile's stored OAuth token and calling the same usage endpoint Claude Code's
// own /usage command uses.
//
// It is strictly read-only: it never refreshes, rewrites, or otherwise touches
// stored credentials. A profile whose access token has gone stale simply reports
// an error, and the caller decides what to do about it.
package claudeusage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL is the Anthropic API host serving the OAuth usage/profile
// endpoints. Overridable on Client for tests.
const DefaultBaseURL = "https://api.anthropic.com"

// DefaultTimeout bounds a single usage probe. Routing a task waits on this, so
// it is deliberately short: a slow or unreachable API should fall back to the
// existing config rather than stall a spawn.
const DefaultTimeout = 10 * time.Second

// Client fetches usage for Claude profiles.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	// CacheDir overrides where snapshots are cached ("" = DefaultCacheDir).
	CacheDir string
	// NoCache bypasses the cache entirely, in both directions.
	NoCache bool
}

// NewClient returns a Client with the default endpoint and timeout.
func NewClient() *Client {
	return &Client{BaseURL: DefaultBaseURL, HTTP: &http.Client{Timeout: DefaultTimeout}}
}

// Limit is one rate-limit window the API reports for an account.
type Limit struct {
	Kind     string     `json:"kind"`     // "session", "weekly_all", "weekly_scoped", …
	Group    string     `json:"group"`    // "session" or "weekly"
	Percent  float64    `json:"percent"`  // 0-100 of this window consumed
	Severity string     `json:"severity"` // provider's own label, e.g. "normal"
	ResetsAt *time.Time `json:"resets_at,omitempty"`
	Scope    string     `json:"scope,omitempty"` // model name for per-model windows
}

// Snapshot is one profile's usage at a point in time.
type Snapshot struct {
	ConfigDir string    `json:"config_dir"`
	Email     string    `json:"email,omitempty"` // filled in only when asked for; a second API call
	Limits    []Limit   `json:"limits"`
	FetchedAt time.Time `json:"fetched_at"`
	// Stale marks a snapshot served from cache after a live fetch failed. The
	// numbers are still worth routing on, but a caller showing them to a person
	// should say so.
	Stale bool `json:"stale,omitempty"`
}

// Age is how long ago this snapshot was read from the API.
func (s Snapshot) Age() time.Duration { return time.Since(s.FetchedAt) }

// UsedPercent is the profile's *binding* constraint: the highest utilization
// across every window the API reports. Routing cares about the window that will
// stop work first, not the average — a 5-hour session window at 98% blocks the
// next task even when the weekly window is nearly untouched.
func (s Snapshot) UsedPercent() float64 {
	worst := 0.0
	for _, l := range s.Limits {
		if l.Percent > worst {
			worst = l.Percent
		}
	}
	return worst
}

// Headroom is how much of the binding window is still available, in percent.
// This is the number to route on: higher wins.
func (s Snapshot) Headroom() float64 { return 100 - s.UsedPercent() }

// BindingLimit returns the window behind UsedPercent, for display and for
// telling the user *when* an exhausted profile frees up.
func (s Snapshot) BindingLimit() (Limit, bool) {
	var worst Limit
	found := false
	for _, l := range s.Limits {
		if !found || l.Percent > worst.Percent {
			worst, found = l, true
		}
	}
	return worst, found
}

// apiUsage mirrors the /api/oauth/usage response. The endpoint carries a good
// deal more (dollar spend, extra-usage credits, unreleased window names); we
// decode only the parts that bear on "can this account do more work right now".
type apiUsage struct {
	Limits   []apiLimit `json:"limits"`
	FiveHour *apiWindow `json:"five_hour"`
	SevenDay *apiWindow `json:"seven_day"`
}

type apiWindow struct {
	Utilization float64    `json:"utilization"`
	ResetsAt    *time.Time `json:"resets_at"`
}

type apiLimit struct {
	Kind     string     `json:"kind"`
	Group    string     `json:"group"`
	Percent  float64    `json:"percent"`
	Severity string     `json:"severity"`
	ResetsAt *time.Time `json:"resets_at"`
	Scope    *struct {
		Model *struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	} `json:"scope"`
}

type apiProfile struct {
	Account struct {
		Email string `json:"email"`
	} `json:"account"`
}

// Fetch reads the usage for one profile (a CLAUDE_CONFIG_DIR), serving a recent
// cached snapshot when there is one.
//
// A missing or expired *credential* fails immediately — that is a configuration
// problem, and papering over it with a cached number would let a profile keep
// receiving tasks it can no longer run. A failed *request* is different: the API
// rate-limits, and a cached snapshot up to StaleTTL old is a far better basis
// for routing than nothing at all.
func (c *Client) Fetch(ctx context.Context, configDir string) (*Snapshot, error) {
	creds, err := LoadCredentials(configDir)
	if err != nil {
		return nil, err
	}
	if creds.Expired() {
		return nil, fmt.Errorf("credentials for %s expired at %s (run a claude session with CLAUDE_CONFIG_DIR=%s to refresh)",
			normalizeDir(configDir), creds.ExpiresAt.Format(time.RFC3339), normalizeDir(configDir))
	}

	if snap, ok := c.readCache(configDir, CacheTTL); ok {
		return snap, nil
	}

	body, err := c.get(ctx, "/api/oauth/usage", creds.AccessToken)
	if err != nil {
		if stale, ok := c.readCache(configDir, StaleTTL); ok {
			stale.Stale = true
			return stale, nil
		}
		return nil, err
	}
	var raw apiUsage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode usage response: %w", err)
	}

	snap := &Snapshot{
		ConfigDir: normalizeDir(configDir),
		Limits:    limitsFrom(raw),
		FetchedAt: time.Now(),
	}
	c.writeCache(configDir, snap)
	return snap, nil
}

// FetchWithAccount is Fetch plus the account email, for surfaces that show the
// user *which* login a profile is. It costs a second round trip, so routing —
// which only needs the numbers — uses plain Fetch. A failure to resolve the
// email is not fatal: the usage numbers are the point.
func (c *Client) FetchWithAccount(ctx context.Context, configDir string) (*Snapshot, error) {
	snap, err := c.Fetch(ctx, configDir)
	if err != nil {
		return nil, err
	}
	if snap.Email != "" {
		return snap, nil // came back from cache with the email already on it
	}
	if email, err := c.Account(ctx, configDir); err == nil && email != "" {
		snap.Email = email
		// Re-cache so the next reader gets the email without a second request.
		if !snap.Stale {
			c.writeCache(configDir, snap)
		}
	}
	return snap, nil
}

// Account returns the email address a profile is logged in as.
func (c *Client) Account(ctx context.Context, configDir string) (string, error) {
	creds, err := LoadCredentials(configDir)
	if err != nil {
		return "", err
	}
	body, err := c.get(ctx, "/api/oauth/profile", creds.AccessToken)
	if err != nil {
		return "", err
	}
	var p apiProfile
	if err := json.Unmarshal(body, &p); err != nil {
		return "", fmt.Errorf("decode profile response: %w", err)
	}
	return p.Account.Email, nil
}

func (c *Client) get(ctx context.Context, path, token string) ([]byte, error) {
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultTimeout}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only GET
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%s: read body: %w", path, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("%s: credentials rejected (401) — this profile needs a fresh login", path)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		// The usage endpoint has its own rate limit, separate from the
		// subscription limits it reports. Name it, so nobody reads this as the
		// account being out of quota.
		return nil, fmt.Errorf("%s: the usage API itself is rate-limiting (429) — this is not the account's quota; retry shortly", path)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: unexpected status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// limitsFrom flattens the API's usage payload into our Limit list. The modern
// response carries a `limits` array; when it is absent or empty we synthesize
// the two headline windows from the older `five_hour`/`seven_day` objects, so a
// rollback on the API side doesn't leave routing blind.
func limitsFrom(raw apiUsage) []Limit {
	if len(raw.Limits) > 0 {
		out := make([]Limit, 0, len(raw.Limits))
		for _, l := range raw.Limits {
			lim := Limit{
				Kind:     l.Kind,
				Group:    l.Group,
				Percent:  l.Percent,
				Severity: l.Severity,
				ResetsAt: l.ResetsAt,
			}
			if l.Scope != nil && l.Scope.Model != nil {
				lim.Scope = l.Scope.Model.DisplayName
			}
			out = append(out, lim)
		}
		return out
	}

	var out []Limit
	if raw.FiveHour != nil {
		out = append(out, Limit{Kind: "session", Group: "session", Percent: raw.FiveHour.Utilization, ResetsAt: raw.FiveHour.ResetsAt})
	}
	if raw.SevenDay != nil {
		out = append(out, Limit{Kind: "weekly_all", Group: "weekly", Percent: raw.SevenDay.Utilization, ResetsAt: raw.SevenDay.ResetsAt})
	}
	return out
}

// Describe renders a one-line human summary of a snapshot, e.g.
// "94% used (session, resets 14:00) — 6% headroom".
func (s Snapshot) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%.0f%% used", s.UsedPercent())
	if l, ok := s.BindingLimit(); ok {
		b.WriteString(" (")
		b.WriteString(limitLabel(l))
		if l.ResetsAt != nil {
			fmt.Fprintf(&b, ", resets %s", l.ResetsAt.Local().Format("Mon 15:04"))
		}
		b.WriteString(")")
	}
	fmt.Fprintf(&b, " — %.0f%% headroom", s.Headroom())
	return b.String()
}

// limitLabel turns an API window kind into something readable.
func limitLabel(l Limit) string {
	label := l.Kind
	switch l.Kind {
	case "session":
		label = "5-hour session"
	case "weekly_all":
		label = "weekly"
	case "weekly_scoped":
		label = "weekly"
		if l.Scope != "" {
			label = "weekly " + l.Scope
		}
	}
	return label
}
