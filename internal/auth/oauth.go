package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/oauth2"
)

// UserInfo represents user information from OAuth providers.
type UserInfo struct {
	Provider          Provider
	ProviderAccountID string
	Email             string
	Name              string
	AvatarURL         string
}

// ExchangeCode exchanges an authorization code for tokens.
func ExchangeCode(ctx context.Context, config *Config, code string) (*oauth2.Token, error) {
	token, err := config.OAuth2.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}
	return token, nil
}

// GetUserInfo fetches user information from the OAuth provider.
func GetUserInfo(ctx context.Context, config *Config, token *oauth2.Token) (*UserInfo, error) {
	switch config.Provider {
	case ProviderGoogle:
		return getGoogleUserInfo(ctx, config, token)
	case ProviderGitHub:
		return getGitHubUserInfo(ctx, config, token)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", config.Provider)
	}
}

// getGoogleUserInfo fetches user info from Google.
func getGoogleUserInfo(ctx context.Context, config *Config, token *oauth2.Token) (*UserInfo, error) {
	client := config.OAuth2.Client(ctx, token)

	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		return nil, fmt.Errorf("get user info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get user info failed: %s - %s", resp.Status, string(body))
	}

	var data struct {
		ID            string `json:"id"`
		Email         string `json:"email"`
		VerifiedEmail bool   `json:"verified_email"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &UserInfo{
		Provider:          ProviderGoogle,
		ProviderAccountID: data.ID,
		Email:             data.Email,
		Name:              data.Name,
		AvatarURL:         data.Picture,
	}, nil
}

// getGitHubUserInfo fetches user info from GitHub.
func getGitHubUserInfo(ctx context.Context, config *Config, token *oauth2.Token) (*UserInfo, error) {
	client := config.OAuth2.Client(ctx, token)

	// Get user profile
	resp, err := client.Get("https://api.github.com/user")
	if err != nil {
		return nil, fmt.Errorf("get user info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get user info failed: %s - %s", resp.Status, string(body))
	}

	var userData struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		Email     string `json:"email"`
		AvatarURL string `json:"avatar_url"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&userData); err != nil {
		return nil, fmt.Errorf("decode user response: %w", err)
	}

	email := userData.Email
	name := userData.Name
	if name == "" {
		name = userData.Login
	}

	// If email is not in profile, fetch from emails API
	if email == "" {
		emailResp, err := client.Get("https://api.github.com/user/emails")
		if err != nil {
			return nil, fmt.Errorf("get user emails: %w", err)
		}
		defer emailResp.Body.Close()

		if emailResp.StatusCode == http.StatusOK {
			var emails []struct {
				Email    string `json:"email"`
				Primary  bool   `json:"primary"`
				Verified bool   `json:"verified"`
			}

			if err := json.NewDecoder(emailResp.Body).Decode(&emails); err == nil {
				// Find primary verified email
				for _, e := range emails {
					if e.Primary && e.Verified {
						email = e.Email
						break
					}
				}
				// Fallback to any verified email
				if email == "" {
					for _, e := range emails {
						if e.Verified {
							email = e.Email
							break
						}
					}
				}
			}
		}
	}

	if email == "" {
		return nil, fmt.Errorf("could not get email from GitHub")
	}

	return &UserInfo{
		Provider:          ProviderGitHub,
		ProviderAccountID: fmt.Sprintf("%d", userData.ID),
		Email:             email,
		Name:              name,
		AvatarURL:         userData.AvatarURL,
	}, nil
}

// TokenExpiry returns the token expiry time, or nil if unknown.
func TokenExpiry(token *oauth2.Token) *time.Time {
	if token.Expiry.IsZero() {
		return nil
	}
	return &token.Expiry
}
