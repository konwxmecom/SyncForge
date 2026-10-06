package access

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
)

var documentIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

type Authorizer interface {
	Allows(token, documentID string) bool
}

type Config struct {
	Principals []Principal `json:"principals"`
}

type Principal struct {
	Token     string   `json:"token"`
	Documents []string `json:"documents"`
}

type tokenAccess struct {
	hash      [sha256.Size]byte
	documents map[string]struct{}
}

type ACL struct {
	principals []tokenAccess
}

func Load(path string) (*ACL, error) {
	if path == "" {
		return nil, errors.New("authorization config path must not be empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect authorization config: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("authorization config must not be readable or writable by group or others")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read authorization config: %w", err)
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("decode authorization config: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("authorization config must contain exactly one JSON value")
	}
	if len(config.Principals) == 0 {
		return nil, errors.New("authorization config must define at least one principal")
	}
	acl := &ACL{principals: make([]tokenAccess, 0, len(config.Principals))}
	seen := make(map[[sha256.Size]byte]struct{}, len(config.Principals))
	for _, principal := range config.Principals {
		if len(principal.Token) < 32 || len(principal.Token) > 512 {
			return nil, errors.New("principal tokens must contain 32 to 512 characters")
		}
		if len(principal.Documents) == 0 {
			return nil, errors.New("each principal must be granted at least one document")
		}
		hash := sha256.Sum256([]byte(principal.Token))
		if _, exists := seen[hash]; exists {
			return nil, errors.New("authorization config contains a duplicate token")
		}
		seen[hash] = struct{}{}
		documents := make(map[string]struct{}, len(principal.Documents))
		for _, documentID := range principal.Documents {
			if !documentIDPattern.MatchString(documentID) {
				return nil, fmt.Errorf("invalid document ID %q in authorization config", documentID)
			}
			if _, exists := documents[documentID]; exists {
				return nil, fmt.Errorf("duplicate document ID %q in authorization config", documentID)
			}
			documents[documentID] = struct{}{}
		}
		acl.principals = append(acl.principals, tokenAccess{hash: hash, documents: documents})
	}
	return acl, nil
}

func (acl *ACL) Allows(token, documentID string) bool {
	if acl == nil || len(token) < 32 || len(token) > 512 {
		return false
	}
	hash := sha256.Sum256([]byte(token))
	allowed := 0
	for _, principal := range acl.principals {
		match := subtle.ConstantTimeCompare(hash[:], principal.hash[:])
		_, hasDocument := principal.documents[documentID]
		allowed |= match & subtle.ConstantTimeSelect(boolToInt(hasDocument), 1, 0)
	}
	return allowed == 1
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

var _ Authorizer = (*ACL)(nil)
