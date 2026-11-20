package nullcode

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const defaultBaseURL = "https://nullaby.org/v1"

// providerAuth is one saved provider: a backend URL and its bearer token.
type providerAuth struct {
	BaseURL string `json:"base_url,omitempty"`
	APIKey  string `json:"api_key,omitempty"`
}

// authConfig is what NullCode persists after the first-launch connect:
// the active provider URL, bearer token, model, and the full provider list,
// in the user config dir.
type authConfig struct {
	BaseURL         string         `json:"base_url,omitempty"`
	APIKey          string         `json:"api_key,omitempty"`
	Model           string         `json:"model,omitempty"`
	Theme           string         `json:"theme,omitempty"`
	Features        []string       `json:"features,omitempty"`
	ToolMode        string         `json:"tool_mode,omitempty"`
	ReasoningEffort string         `json:"reasoning_effort,omitempty"`
	ContextTokens   int            `json:"context_tokens,omitempty"`
	Providers       []providerAuth `json:"providers,omitempty"`
}

func authPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nullcode", "config.json"), nil
}

func loadAuthFrom(path string) (authConfig, bool) {
	if path == "" {
		return authConfig{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return authConfig{}, false
	}
	var c authConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return authConfig{}, false
	}
	// Older configs saved a single provider without the list.
	if len(c.Providers) == 0 && c.BaseURL != "" && c.APIKey != "" {
		c.Providers = []providerAuth{{BaseURL: c.BaseURL, APIKey: c.APIKey}}
	}
	return c, true
}

func saveAuthTo(path string, c authConfig) error {
	if path == "" {
		return errors.New("no user config directory available")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

func maskKey(k string) string {
	if k == "" {
		return "(none)"
	}
	if len(k) <= 8 {
		return "••••••"
	}
	return "••••••" + k[len(k)-4:]
}
