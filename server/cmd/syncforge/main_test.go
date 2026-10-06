package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
