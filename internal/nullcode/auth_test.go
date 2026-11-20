package nullcode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuthRoundtrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if _, ok := loadAuthFrom(p); ok {
		t.Fatal("missing config should not load")
	}
	if err := saveAuthTo(p, authConfig{BaseURL: "http://x:1/v1", APIKey: "secret", Model: "m1", Theme: "dark"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	c, ok := loadAuthFrom(p)
	if !ok || c.BaseURL != "http://x:1/v1" || c.APIKey != "secret" || c.Model != "m1" || c.Theme != "dark" {
		t.Fatalf("loaded = %+v ok=%v", c, ok)
	}
	if err := os.WriteFile(p, []byte("{bad json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadAuthFrom(p); ok {
		t.Fatal("corrupt config should not load")
	}
	if err := saveAuthTo("", authConfig{}); err == nil {
		t.Fatal("empty path should error")
	}
}

func TestMaskKey(t *testing.T) {
	if got := maskKey(""); got != "(none)" {
		t.Fatalf("empty = %q", got)
	}
	if got := maskKey("short"); got != "••••••" {
		t.Fatalf("short = %q", got)
	}
	if got := maskKey("sk-1234567890abcd"); got != "••••••abcd" {
		t.Fatalf("long = %q", got)
	}
}
