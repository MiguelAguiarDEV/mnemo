package auth

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Gentleman-Programming/engram/internal/cloud"
	"github.com/Gentleman-Programming/engram/internal/cloud/cloudstore"
	"github.com/golang-jwt/jwt/v5"
	_ "github.com/lib/pq"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
)

// ── Test Helpers ─────────────────────────────────────────────────────────────

const testSecret = "this-is-a-test-secret-that-is-at-least-32-bytes-long!"

// testStore creates a real Postgres container and returns a CloudStore.
func testStore(t *testing.T) *cloudstore.CloudStore {
	t.Helper()

	if os.Getenv("SKIP_DOCKER_TESTS") == "1" {
		t.Skip("SKIP_DOCKER_TESTS=1, skipping dockertest-based test")
	}

	pool, err := dockertest.NewPool("")
	if err != nil {
		t.Fatalf("could not construct dockertest pool: %v", err)
	}
	if err := pool.Client.Ping(); err != nil {
		t.Fatalf("could not connect to Docker: %v", err)
	}

	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres",
		Tag:        "16-alpine",
		Env: []string{
			"POSTGRES_PASSWORD=test",
			"POSTGRES_DB=engram_test",
			"POSTGRES_USER=postgres",
		},
	}, func(config *docker.HostConfig) {
		config.AutoRemove = true
		config.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	if err != nil {
		t.Fatalf("could not start postgres container: %v", err)
	}

	t.Cleanup(func() {
		_ = pool.Purge(resource)
	})

	dsn := fmt.Sprintf("postgres://postgres:test@localhost:%s/engram_test?sslmode=disable",
		resource.GetPort("5432/tcp"))

	if err := pool.Retry(func() error {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			return err
		}
		defer db.Close()
		return db.Ping()
	}); err != nil {
		t.Fatalf("could not connect to postgres: %v", err)
	}

	cs, err := cloudstore.New(cloud.Config{DSN: dsn, MaxPool: 5})
	if err != nil {
		t.Fatalf("cloudstore.New: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func testService(t *testing.T) (*Service, *cloudstore.CloudStore) {
	t.Helper()
	store := testStore(t)
	svc, err := NewService(store, testSecret)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, store
}

// ── NewService ───────────────────────────────────────────────────────────────

func TestNewService_ValidSecret(t *testing.T) {
	// NewService doesn't need a real store for this test, but we pass nil
	// since we're only testing secret validation.
	svc, err := NewService(nil, testSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc == nil {
		t.Fatal("service should not be nil")
	}
}

func TestNewService_ShortSecret(t *testing.T) {
	_, err := NewService(nil, "too-short")
	if err != ErrSecretTooShort {
		t.Errorf("expected ErrSecretTooShort, got %v", err)
	}
}

func TestNewService_Exactly32Bytes(t *testing.T) {
	secret := strings.Repeat("a", 32)
	svc, err := NewService(nil, secret)
	if err != nil {
		t.Fatalf("32-byte secret should be valid, got: %v", err)
	}
	if svc == nil {
		t.Fatal("service should not be nil")
	}
}

func TestNewService_31Bytes(t *testing.T) {
	secret := strings.Repeat("a", 31)
	_, err := NewService(nil, secret)
	if err != ErrSecretTooShort {
		t.Errorf("31-byte secret should fail with ErrSecretTooShort, got %v", err)
	}
}

// ── JWT Generation ───────────────────────────────────────────────────────────

func TestGenerateTokenPair_ValidClaims(t *testing.T) {
	svc, err := NewService(nil, testSecret)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	accessToken, refreshToken, err := svc.GenerateTokenPair("user-123", "bob")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	if accessToken == "" {
		t.Error("access token should not be empty")
	}
	if refreshToken == "" {
		t.Error("refresh token should not be empty")
	}
	if accessToken == refreshToken {
		t.Error("access and refresh tokens should be different")
	}

	// Parse access token and verify claims.
	parser := jwt.NewParser()
	claims := &Claims{}
	token, err := parser.ParseWithClaims(accessToken, claims, func(t *jwt.Token) (any, error) {
		return []byte(testSecret), nil
	})
	if err != nil {
		t.Fatalf("parse access token: %v", err)
	}
	if !token.Valid {
		t.Error("access token should be valid")
	}
	if claims.UserID != "user-123" {
		t.Errorf("user_id = %q, want user-123", claims.UserID)
	}
	if claims.Username != "bob" {
		t.Errorf("username = %q, want bob", claims.Username)
	}
	if claims.Type != "access" {
		t.Errorf("type = %q, want access", claims.Type)
	}
	if claims.Subject != "user-123" {
		t.Errorf("sub = %q, want user-123", claims.Subject)
	}

	// Verify expiry is approximately 1 hour from now.
	expiry := claims.ExpiresAt.Time
	expectedExpiry := time.Now().Add(1 * time.Hour)
	if expiry.Before(expectedExpiry.Add(-1*time.Minute)) || expiry.After(expectedExpiry.Add(1*time.Minute)) {
		t.Errorf("access token expiry %v is not within 1 minute of expected %v", expiry, expectedExpiry)
	}

	// Parse refresh token and verify claims.
	refreshClaims := &Claims{}
	rToken, err := parser.ParseWithClaims(refreshToken, refreshClaims, func(t *jwt.Token) (any, error) {
		return []byte(testSecret), nil
	})
	if err != nil {
		t.Fatalf("parse refresh token: %v", err)
	}
	if !rToken.Valid {
		t.Error("refresh token should be valid")
	}
	if refreshClaims.Type != "refresh" {
		t.Errorf("refresh type = %q, want refresh", refreshClaims.Type)
	}
	if refreshClaims.UserID != "user-123" {
		t.Errorf("refresh user_id = %q, want user-123", refreshClaims.UserID)
	}

	// Verify refresh expiry is approximately 7 days from now.
	refreshExpiry := refreshClaims.ExpiresAt.Time
	expectedRefreshExpiry := time.Now().Add(7 * 24 * time.Hour)
	if refreshExpiry.Before(expectedRefreshExpiry.Add(-1*time.Minute)) || refreshExpiry.After(expectedRefreshExpiry.Add(1*time.Minute)) {
		t.Errorf("refresh token expiry %v is not within 1 minute of expected %v", refreshExpiry, expectedRefreshExpiry)
	}
}

func TestGenerateTokenPair_HMACSha256(t *testing.T) {
	svc, err := NewService(nil, testSecret)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	accessToken, _, err := svc.GenerateTokenPair("u1", "alice")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	// Parse without validation to check the header.
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, _, err := parser.ParseUnverified(accessToken, &Claims{})
	if err != nil {
		t.Fatalf("ParseUnverified: %v", err)
	}
	if token.Method.Alg() != "HS256" {
		t.Errorf("signing method = %q, want HS256", token.Method.Alg())
	}
}

// ── JWT Validation ───────────────────────────────────────────────────────────

func TestValidateAccessToken_Valid(t *testing.T) {
	svc, err := NewService(nil, testSecret)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	accessToken, _, err := svc.GenerateTokenPair("user-123", "bob")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	claims, err := svc.ValidateAccessToken(accessToken)
	if err != nil {
		t.Fatalf("ValidateAccessToken: %v", err)
	}
	if claims.UserID != "user-123" {
		t.Errorf("user_id = %q, want user-123", claims.UserID)
	}
	if claims.Username != "bob" {
		t.Errorf("username = %q, want bob", claims.Username)
	}
	if claims.Type != "access" {
		t.Errorf("type = %q, want access", claims.Type)
	}
}

func TestValidateAccessToken_Expired(t *testing.T) {
	svc, err := NewService(nil, testSecret)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// Create an expired token manually.
	now := time.Now().Add(-2 * time.Hour)
	claims := Claims{
		UserID:   "user-123",
		Username: "bob",
		Type:     "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-123",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(1 * time.Hour)), // expired 1 hour ago
		},
	}
	expiredToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign expired token: %v", err)
	}

	_, err = svc.ValidateAccessToken(expiredToken)
	if err != ErrTokenExpired {
		t.Errorf("expected ErrTokenExpired, got %v", err)
	}
}

func TestValidateAccessToken_WrongType(t *testing.T) {
	svc, err := NewService(nil, testSecret)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// Generate a token pair and try to validate the refresh token as access.
	_, refreshToken, err := svc.GenerateTokenPair("user-123", "bob")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	_, err = svc.ValidateAccessToken(refreshToken)
	if err != ErrWrongTokenType {
		t.Errorf("expected ErrWrongTokenType, got %v", err)
	}
}

func TestValidateAccessToken_TamperedSignature(t *testing.T) {
	svc, err := NewService(nil, testSecret)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	accessToken, _, err := svc.GenerateTokenPair("user-123", "bob")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	// Tamper with the signature by flipping multiple characters.
	// Changing only the last char may not alter decoded bytes due to base64 padding.
	parts := strings.SplitN(accessToken, ".", 3)
	sig := parts[2]
	flipped := []byte(sig)
	for i := 0; i < len(flipped) && i < 10; i++ {
		flipped[i] = 'A'
	}
	tampered := parts[0] + "." + parts[1] + "." + string(flipped)

	_, err = svc.ValidateAccessToken(tampered)
	if err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestValidateAccessToken_DifferentSecret(t *testing.T) {
	svc1, err := NewService(nil, testSecret)
	if err != nil {
		t.Fatalf("NewService svc1: %v", err)
	}
	svc2, err := NewService(nil, strings.Repeat("b", 32))
	if err != nil {
		t.Fatalf("NewService svc2: %v", err)
	}

	accessToken, _, err := svc1.GenerateTokenPair("user-123", "bob")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	_, err = svc2.ValidateAccessToken(accessToken)
	if err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken when using different secret, got %v", err)
	}
}

func TestValidateAccessToken_GarbageInput(t *testing.T) {
	svc, err := NewService(nil, testSecret)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	_, err = svc.ValidateAccessToken("not-a-jwt-at-all")
	if err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

// ── Token Refresh ────────────────────────────────────────────────────────────

func TestRefreshAccessToken_Valid(t *testing.T) {
	svc, err := NewService(nil, testSecret)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	_, refreshToken, err := svc.GenerateTokenPair("user-123", "bob")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	newAccess, err := svc.RefreshAccessToken(refreshToken)
	if err != nil {
		t.Fatalf("RefreshAccessToken: %v", err)
	}
	if newAccess == "" {
		t.Error("new access token should not be empty")
	}

	// Validate the new access token.
	claims, err := svc.ValidateAccessToken(newAccess)
	if err != nil {
		t.Fatalf("ValidateAccessToken on refreshed token: %v", err)
	}
	if claims.UserID != "user-123" {
		t.Errorf("refreshed user_id = %q, want user-123", claims.UserID)
	}
	if claims.Username != "bob" {
		t.Errorf("refreshed username = %q, want bob", claims.Username)
	}
	if claims.Type != "access" {
		t.Errorf("refreshed type = %q, want access", claims.Type)
	}
}

func TestRefreshAccessToken_ExpiredRefresh(t *testing.T) {
	svc, err := NewService(nil, testSecret)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// Create an expired refresh token manually.
	now := time.Now().Add(-8 * 24 * time.Hour)
	claims := Claims{
		UserID:   "user-123",
		Username: "bob",
		Type:     "refresh",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-123",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(7 * 24 * time.Hour)), // expired 1 day ago
		},
	}
	expiredRefresh, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign expired refresh: %v", err)
	}

	_, err = svc.RefreshAccessToken(expiredRefresh)
	if err != ErrTokenExpired {
		t.Errorf("expected ErrTokenExpired, got %v", err)
	}
}

func TestRefreshAccessToken_AccessTokenAsRefresh(t *testing.T) {
	svc, err := NewService(nil, testSecret)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	accessToken, _, err := svc.GenerateTokenPair("user-123", "bob")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	// Using an access token as a refresh token should fail.
	_, err = svc.RefreshAccessToken(accessToken)
	if err != ErrWrongTokenType {
		t.Errorf("expected ErrWrongTokenType when using access as refresh, got %v", err)
	}
}

// ── Registration (requires Postgres via dockertest) ──────────────────────────

func TestRegister_Success(t *testing.T) {
	svc, _ := testService(t)

	result, err := svc.Register("alice", "alice@example.com", "MyStr0ngP@ss")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if result.UserID == "" {
		t.Error("user_id should not be empty")
	}
	if result.Username != "alice" {
		t.Errorf("username = %q, want alice", result.Username)
	}
	if result.AccessToken == "" {
		t.Error("access_token should not be empty")
	}
	if result.RefreshToken == "" {
		t.Error("refresh_token should not be empty")
	}
	if result.ExpiresIn != 3600 {
		t.Errorf("expires_in = %d, want 3600", result.ExpiresIn)
	}

	// Verify the access token is valid.
	claims, err := svc.ValidateAccessToken(result.AccessToken)
	if err != nil {
		t.Fatalf("ValidateAccessToken on registered user: %v", err)
	}
	if claims.UserID != result.UserID {
		t.Errorf("token user_id = %q, want %q", claims.UserID, result.UserID)
	}
}

func TestRegister_DuplicateUsername(t *testing.T) {
	svc, _ := testService(t)

	_, err := svc.Register("alice", "alice@example.com", "MyStr0ngP@ss")
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}

	_, err = svc.Register("alice", "different@example.com", "An0therP@ss!")
	if err == nil {
		t.Fatal("expected error for duplicate username, got nil")
	}
}

func TestRegister_WeakPassword(t *testing.T) {
	svc, _ := testService(t)

	_, err := svc.Register("bob", "bob@example.com", "123")
	if err != ErrWeakPassword {
		t.Errorf("expected ErrWeakPassword, got %v", err)
	}
}

func TestRegister_PasswordExactly8Chars(t *testing.T) {
	svc, _ := testService(t)

	result, err := svc.Register("charlie", "charlie@example.com", "12345678")
	if err != nil {
		t.Fatalf("8-char password should be accepted, got: %v", err)
	}
	if result.UserID == "" {
		t.Error("user_id should not be empty")
	}
}

// ── Login (requires Postgres via dockertest) ─────────────────────────────────

func TestLogin_Success(t *testing.T) {
	svc, _ := testService(t)

	// Register first.
	_, err := svc.Register("alice", "alice@example.com", "MyStr0ngP@ss")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Login.
	result, err := svc.Login("alice", "MyStr0ngP@ss")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if result.UserID == "" {
		t.Error("user_id should not be empty")
	}
	if result.Username != "alice" {
		t.Errorf("username = %q, want alice", result.Username)
	}
	if result.AccessToken == "" {
		t.Error("access_token should not be empty")
	}
	if result.RefreshToken == "" {
		t.Error("refresh_token should not be empty")
	}
	if result.ExpiresIn != 3600 {
		t.Errorf("expires_in = %d, want 3600", result.ExpiresIn)
	}
}

func TestLogin_SuccessWithEmail(t *testing.T) {
	svc, _ := testService(t)

	_, err := svc.Register("alice", "alice@example.com", "MyStr0ngP@ss")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	result, err := svc.Login("alice@example.com", "MyStr0ngP@ss")
	if err != nil {
		t.Fatalf("Login with email: %v", err)
	}

	if result.Username != "alice" {
		t.Errorf("username = %q, want alice", result.Username)
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	svc, _ := testService(t)

	_, err := svc.Register("alice", "alice@example.com", "MyStr0ngP@ss")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, err = svc.Login("alice", "wrongpassword")
	if err != ErrInvalidCredentials {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestLogin_NonExistentUser(t *testing.T) {
	svc, _ := testService(t)

	_, err := svc.Login("nonexistent", "somepassword")
	if err != ErrInvalidCredentials {
		t.Errorf("expected ErrInvalidCredentials for non-existent user, got %v", err)
	}
}

func TestLogin_WrongPasswordSameErrorAsNonExistent(t *testing.T) {
	svc, _ := testService(t)

	_, err := svc.Register("alice", "alice@example.com", "MyStr0ngP@ss")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Both wrong password and non-existent user should return the same error.
	_, errWrongPw := svc.Login("alice", "wrongpassword")
	_, errNoUser := svc.Login("nobody", "doesntmatter")

	if errWrongPw != ErrInvalidCredentials {
		t.Errorf("wrong password: expected ErrInvalidCredentials, got %v", errWrongPw)
	}
	if errNoUser != ErrInvalidCredentials {
		t.Errorf("non-existent user: expected ErrInvalidCredentials, got %v", errNoUser)
	}
	// Both errors should be identical (same error, same message).
	if errWrongPw.Error() != errNoUser.Error() {
		t.Errorf("error messages differ: %q vs %q — this could leak username existence", errWrongPw.Error(), errNoUser.Error())
	}
}

// ── API Key (pure Go tests) ─────────────────────────────────────────────────

func TestGenerateAPIKey_Format(t *testing.T) {
	key, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}

	if !strings.HasPrefix(key, "eng_") {
		t.Errorf("key should start with eng_, got %q", key[:10])
	}

	// eng_ (4 chars) + 64 hex chars (32 bytes) = 68 chars total.
	if len(key) != 68 {
		t.Errorf("key length = %d, want 68", len(key))
	}

	if hash == "" {
		t.Error("hash should not be empty")
	}

	// Hash should be a 64-char hex string (SHA-256).
	if len(hash) != 64 {
		t.Errorf("hash length = %d, want 64", len(hash))
	}
}

func TestGenerateAPIKey_Unique(t *testing.T) {
	key1, _, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey 1: %v", err)
	}
	key2, _, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey 2: %v", err)
	}

	if key1 == key2 {
		t.Error("two generated keys should not be equal")
	}
}

func TestGenerateAPIKey_HashDeterministic(t *testing.T) {
	key, hash1, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}

	// Hashing the same key should produce the same hash.
	hash2 := hashAPIKey(key)
	if hash1 != hash2 {
		t.Errorf("hash mismatch: %q vs %q", hash1, hash2)
	}
}

// ── API Key Validation (requires Postgres via dockertest) ────────────────────

func TestValidateAPIKey_Success(t *testing.T) {
	_, store := testService(t)

	// Create a user.
	user, err := store.CreateUser("alice", "alice@example.com", "MyStr0ngP@ss")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Generate an API key and store its hash.
	plainKey, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if err := store.SetAPIKeyHash(user.ID, hash); err != nil {
		t.Fatalf("SetAPIKeyHash: %v", err)
	}

	// Validate.
	found, err := ValidateAPIKey(store, plainKey)
	if err != nil {
		t.Fatalf("ValidateAPIKey: %v", err)
	}
	if found.ID != user.ID {
		t.Errorf("found user ID = %q, want %q", found.ID, user.ID)
	}
	if found.Username != "alice" {
		t.Errorf("found username = %q, want alice", found.Username)
	}
}

func TestValidateAPIKey_RevokedKey(t *testing.T) {
	_, store := testService(t)

	user, err := store.CreateUser("alice", "alice@example.com", "MyStr0ngP@ss")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Generate and store key.
	plainKey, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if err := store.SetAPIKeyHash(user.ID, hash); err != nil {
		t.Fatalf("SetAPIKeyHash: %v", err)
	}

	// Revoke by setting hash to empty.
	if err := store.SetAPIKeyHash(user.ID, ""); err != nil {
		t.Fatalf("SetAPIKeyHash (revoke): %v", err)
	}

	// Validation should fail.
	_, err = ValidateAPIKey(store, plainKey)
	if err != ErrInvalidCredentials {
		t.Errorf("expected ErrInvalidCredentials for revoked key, got %v", err)
	}
}

func TestValidateAPIKey_InvalidKey(t *testing.T) {
	_, store := testService(t)

	// No user has this key.
	_, err := ValidateAPIKey(store, "eng_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != ErrInvalidCredentials {
		t.Errorf("expected ErrInvalidCredentials for invalid key, got %v", err)
	}
}
