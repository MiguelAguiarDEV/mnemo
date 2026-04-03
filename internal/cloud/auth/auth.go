package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Gentleman-Programming/engram/internal/cloud/cloudstore"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// ─── Errors ──────────────────────────────────────────────────────────────────

var (
	// ErrInvalidCredentials is returned for failed login attempts. The message
	// is deliberately generic to avoid leaking whether the username exists.
	ErrInvalidCredentials = errors.New("invalid credentials")

	// ErrWeakPassword is returned when a password is shorter than 8 characters.
	ErrWeakPassword = errors.New("password must be at least 8 characters")

	// ErrSecretTooShort is returned when the JWT secret is shorter than 32 bytes.
	ErrSecretTooShort = errors.New("jwt secret must be at least 32 bytes")

	// ErrInvalidToken is returned for any token validation failure.
	ErrInvalidToken = errors.New("invalid token")

	// ErrTokenExpired is returned when a token has expired.
	ErrTokenExpired = errors.New("token has expired")

	// ErrWrongTokenType is returned when a token's type claim doesn't match expectations.
	ErrWrongTokenType = errors.New("wrong token type")
)

// ─── Claims ──────────────────────────────────────────────────────────────────

// Claims represents the custom JWT claims for Engram Cloud tokens.
type Claims struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Type     string `json:"type"` // "access" or "refresh"
	jwt.RegisteredClaims
}

// ─── AuthResult ──────────────────────────────────────────────────────────────

// AuthResult is returned by Register and Login on success.
type AuthResult struct {
	UserID       string `json:"user_id"`
	Username     string `json:"username"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"` // seconds until access token expires
}

// ─── Service ─────────────────────────────────────────────────────────────────

// Service handles JWT authentication and user registration/login for Engram Cloud.
type Service struct {
	secret []byte
	store  *cloudstore.CloudStore
}

// NewService creates a new auth Service. The jwtSecret MUST be at least 32 bytes.
func NewService(store *cloudstore.CloudStore, jwtSecret string) (*Service, error) {
	if len(jwtSecret) < 32 {
		return nil, ErrSecretTooShort
	}
	return &Service{
		secret: []byte(jwtSecret),
		store:  store,
	}, nil
}

// ─── Token Generation ────────────────────────────────────────────────────────

// accessTokenExpiry is the lifetime of an access token.
const accessTokenExpiry = 1 * time.Hour

// refreshTokenExpiry is the lifetime of a refresh token.
const refreshTokenExpiry = 7 * 24 * time.Hour

// GenerateTokenPair creates an access token (1h) and refresh token (7d) for
// the given user. Both are signed with HMAC-SHA256.
func (s *Service) GenerateTokenPair(userID, username string) (accessToken, refreshToken string, err error) {
	now := time.Now()

	// Access token.
	accessClaims := Claims{
		UserID:   userID,
		Username: username,
		Type:     "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(accessTokenExpiry)),
		},
	}
	accessToken, err = jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims).SignedString(s.secret)
	if err != nil {
		return "", "", fmt.Errorf("auth: sign access token: %w", err)
	}

	// Refresh token.
	refreshClaims := Claims{
		UserID:   userID,
		Username: username,
		Type:     "refresh",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(refreshTokenExpiry)),
		},
	}
	refreshToken, err = jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims).SignedString(s.secret)
	if err != nil {
		return "", "", fmt.Errorf("auth: sign refresh token: %w", err)
	}

	return accessToken, refreshToken, nil
}

// ─── Token Validation ────────────────────────────────────────────────────────

// ValidateAccessToken parses and validates an access token string. It checks
// the HMAC-SHA256 signature, expiry, and verifies that the token type is "access".
func (s *Service) ValidateAccessToken(tokenStr string) (*Claims, error) {
	claims, err := s.parseToken(tokenStr)
	if err != nil {
		return nil, err
	}
	if claims.Type != "access" {
		return nil, ErrWrongTokenType
	}
	return claims, nil
}

// RefreshAccessToken validates a refresh token and issues a new access token.
// It verifies that the token type is "refresh", then generates a fresh access
// token for the same user.
func (s *Service) RefreshAccessToken(refreshTokenStr string) (newAccessToken string, err error) {
	claims, err := s.parseToken(refreshTokenStr)
	if err != nil {
		return "", err
	}
	if claims.Type != "refresh" {
		return "", ErrWrongTokenType
	}

	// Generate only a new access token (not a full pair).
	now := time.Now()
	accessClaims := Claims{
		UserID:   claims.UserID,
		Username: claims.Username,
		Type:     "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   claims.UserID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(accessTokenExpiry)),
		},
	}
	newAccessToken, err = jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims).SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("auth: sign refreshed access token: %w", err)
	}
	return newAccessToken, nil
}

// parseToken parses a JWT string, validating signature and expiry.
func (s *Service) parseToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrInvalidToken
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// ─── Registration & Login ────────────────────────────────────────────────────

// Register creates a new user and returns an AuthResult with JWT tokens.
// Password must be at least 8 characters.
func (s *Service) Register(username, email, password string) (*AuthResult, error) {
	if len(password) < 8 {
		return nil, ErrWeakPassword
	}

	user, err := s.store.CreateUser(username, email, password)
	if err != nil {
		return nil, fmt.Errorf("auth: register: %w", err)
	}

	accessToken, refreshToken, err := s.GenerateTokenPair(user.ID, user.Username)
	if err != nil {
		return nil, err
	}

	return &AuthResult{
		UserID:       user.ID,
		Username:     user.Username,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int(accessTokenExpiry.Seconds()),
	}, nil
}

// Login authenticates a user by username or email and password, returning JWT
// tokens. On any failure (wrong password, nonexistent user), it returns
// ErrInvalidCredentials with no information about which part failed. Uses
// bcrypt.CompareHashAndPassword which is inherently constant-time.
func (s *Service) Login(identifier, password string) (*AuthResult, error) {
	user, err := s.lookupUserForLogin(identifier)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("auth: login lookup: %w", err)
		}
		// User not found — perform a dummy bcrypt compare to prevent timing attacks
		// that could reveal whether the username exists.
		bcrypt.CompareHashAndPassword(
			[]byte("$2a$10$xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"),
			[]byte(password),
		)
		return nil, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	accessToken, refreshToken, err := s.GenerateTokenPair(user.ID, user.Username)
	if err != nil {
		return nil, err
	}

	return &AuthResult{
		UserID:       user.ID,
		Username:     user.Username,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int(accessTokenExpiry.Seconds()),
	}, nil
}

func (s *Service) lookupUserForLogin(identifier string) (*cloudstore.CloudUser, error) {
	user, err := s.store.GetUserByUsername(identifier)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	return s.store.GetUserByEmail(identifier)
}
