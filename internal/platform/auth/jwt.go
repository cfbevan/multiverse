package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenManager signs and validates JWT tokens for authenticated requests.
type TokenManager struct {
	secret []byte
}

const minJWTSecretLength = 16

// Claims contains the custom JWT claims used by the app.
type Claims struct {
	jwt.RegisteredClaims

	UserID int64 `json:"uid"`
}

// NewTokenManager creates a token manager from the configured JWT secret.
func NewTokenManager(secret string) (*TokenManager, error) {
	if len(secret) < minJWTSecretLength {
		return nil, errors.New("jwt secret must be at least 16 characters")
	}

	return &TokenManager{secret: []byte(secret)}, nil
}

// Issue creates a signed token for the supplied user ID and lifetime.
func (m *TokenManager) Issue(userID int64, ttl time.Duration) (string, error) {
	now := time.Now().UTC()
	claims := Claims{
		UserID:    userID,
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	return token.SignedString(m.secret)
}

// Parse validates and decodes a JWT token.
func (m *TokenManager) Parse(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}

		return m.secret, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}

	return claims, nil
}
