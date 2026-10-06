package access

import (
	"os"
	"path/filepath"
	"testing"
)

func TestACLRestrictsPrincipalsToGrantedDocuments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	raw := `{"principals":[{"token":"01234567890123456789012345678901","documents":["shared-doc"]}]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	acl, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !acl.Allows("01234567890123456789012345678901", "shared-doc") {
		t.Fatal("valid principal was denied access")
	}
	if acl.Allows("01234567890123456789012345678901", "other-doc") {
		t.Fatal("principal accessed an ungranted document")
	}
	if acl.Allows("wrong-token", "shared-doc") {
		t.Fatal("invalid token was authorized")
	}
}

func TestLoadRejectsWeakAndOverexposedAuthorizationConfig(t *testing.T) {
	for name, testCase := range map[string]struct {
		content string
		mode    os.FileMode
	}{
		"weak token": {
			content: `{"principals":[{"token":"short","documents":["doc"]}]}`,
			mode:    0o600,
		},
		"permissive permissions": {
			content: `{"principals":[{"token":"01234567890123456789012345678901","documents":["doc"]}]}`,
			mode:    0o644,
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			if err := os.WriteFile(path, []byte(testCase.content), testCase.mode); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("Load succeeded, want error")
			}
		})
	}
}
