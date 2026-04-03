package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/Gentleman-Programming/engram/internal/cloud/cloudstore"
)

// apiKeyPrefix is the prefix for all Engram API keys.
const apiKeyPrefix = "eng_"

// GenerateAPIKey creates a new API key with the eng_ prefix and returns both
// the plain key (for display to the user) and its SHA-256 hash (for storage).
// Uses crypto/rand for cryptographically secure random bytes.
func GenerateAPIKey() (plainKey, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("auth: generate api key: %w", err)
	}
	plainKey = apiKeyPrefix + hex.EncodeToString(b)
	hash = hashAPIKey(plainKey)
	return plainKey, hash, nil
}

// ValidateAPIKey validates an API key by hashing it with SHA-256 and looking
// up the user by api_key_hash in the store.
func ValidateAPIKey(store *cloudstore.CloudStore, key string) (*cloudstore.CloudUser, error) {
	hash := hashAPIKey(key)
	user, err := store.GetUserByAPIKeyHash(hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	return user, nil
}

// hashAPIKey returns the SHA-256 hex digest of an API key.
func hashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}
