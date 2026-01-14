// Package auth provides OAuth2 authentication for the web UI.
package auth

import (
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
	"golang.org/x/oauth2/google"
)

// Provider represents an OAuth2 provider.
type Provider string

const (
	ProviderGoogle Provider = "google"
	ProviderGitHub Provider = "github"
)

// Config holds OAuth configuration for a provider.
type Config struct {
	OAuth2   *oauth2.Config
	Provider Provider
}

// GoogleConfig returns the OAuth2 config for Google.
func GoogleConfig(redirectURL string) *Config {
	return &Config{
		Provider: ProviderGoogle,
		OAuth2: &oauth2.Config{
			ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
			ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
			RedirectURL:  redirectURL,
			Scopes: []string{
				"https://www.googleapis.com/auth/userinfo.email",
				"https://www.googleapis.com/auth/userinfo.profile",
			},
			Endpoint: google.Endpoint,
		},
	}
}

// GitHubConfig returns the OAuth2 config for GitHub.
func GitHubConfig(redirectURL string) *Config {
	return &Config{
		Provider: ProviderGitHub,
		OAuth2: &oauth2.Config{
			ClientID:     os.Getenv("GITHUB_CLIENT_ID"),
			ClientSecret: os.Getenv("GITHUB_CLIENT_SECRET"),
			RedirectURL:  redirectURL,
			Scopes: []string{
				"user:email",
				"read:user",
			},
			Endpoint: github.Endpoint,
		},
	}
}

// GetConfig returns the OAuth2 config for the specified provider.
func GetConfig(provider Provider, redirectURL string) *Config {
	switch provider {
	case ProviderGoogle:
		return GoogleConfig(redirectURL)
	case ProviderGitHub:
		return GitHubConfig(redirectURL)
	default:
		return nil
	}
}

// IsConfigured checks if a provider has valid credentials configured.
func IsConfigured(provider Provider) bool {
	switch provider {
	case ProviderGoogle:
		return os.Getenv("GOOGLE_CLIENT_ID") != "" && os.Getenv("GOOGLE_CLIENT_SECRET") != ""
	case ProviderGitHub:
		return os.Getenv("GITHUB_CLIENT_ID") != "" && os.Getenv("GITHUB_CLIENT_SECRET") != ""
	default:
		return false
	}
}
