package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konwxmecom/SyncForge/server/internal/syncserver"
)

func TestLoadAuthorizerRequiresConfig(t *testing.T) {
	for _, authFile := range []string{"", " \t"} {
		if _, err := loadAuthorizer(authFile); err == nil || !strings.Contains(err.Error(), "AUTH_FILE is required") {
			t.Fatalf("loadAuthorizer(%q) error = %v, want required AUTH_FILE error", authFile, err)
		}
	}
}

func TestLoadAuthorizerAcceptsRestrictedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	raw := `{"principals":[{"token":"01234567890123456789012345678901","documents":["demo-room"]}]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	authorizer, err := loadAuthorizer(path)
	if err != nil {
		t.Fatal(err)
	}
	if !authorizer.Allows("01234567890123456789012345678901", "demo-room") {
		t.Fatal("configured token was not authorized for its document")
	}
}

func TestLoadLimitsDefaultsAndOverrides(t *testing.T) {
	defaults, err := loadLimits(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if defaults != syncserver.DefaultLimits() {
		t.Fatalf("default limits = %+v, want %+v", defaults, syncserver.DefaultLimits())
	}

	values := map[string]string{
		"MAX_CONNECTIONS":              "80",
		"MAX_CONNECTIONS_PER_DOCUMENT": "20",
		"MAX_MESSAGES_PER_MINUTE":      "1200",
	}
	overrides, err := loadLimits(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	want := syncserver.Limits{
		MaxConnections:            80,
		MaxConnectionsPerDocument: 20,
		MaxMessagesPerMinute:      1200,
	}
	if overrides != want {
		t.Fatalf("overridden limits = %+v, want %+v", overrides, want)
	}
}

func TestLoadLimitsRejectsInvalidValues(t *testing.T) {
	for name, values := range map[string]map[string]string{
		"non-integer": {
			"MAX_CONNECTIONS": "many",
		},
		"non-positive": {
			"MAX_CONNECTIONS": "0",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadLimits(func(key string) string { return values[key] }); err == nil {
				t.Fatal("loadLimits succeeded, want an error")
			}
		})
	}
}
