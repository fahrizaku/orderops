// Package authn verifies JWTs issued by the auth service. Both services
// share one HS256 secret (a deliberate simplicity for this platform's size;
// asymmetric keys with a JWKS endpoint would be the next step).
package authn

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/golang-jwt/jwt/v5"
)

var ErrInvalidToken = errors.New("invalid token")

// Caller is the authenticated identity behind a request.
type Caller struct {
	ID   int64
	Role string // customer | courier | admin, as issued by the auth service
}

func (c Caller) IsStaff() bool {
	return c.Role == "courier" || c.Role == "admin"
}

type claims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// Verifier checks tokens against the shared secret.
type Verifier struct {
	secret []byte
}

func NewVerifier(secret string) *Verifier {
	return &Verifier{secret: []byte(secret)}
}

func (v *Verifier) Verify(token string) (Caller, error) {
	var c claims
	_, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return v.secret, nil
	})
	if err != nil {
		return Caller{}, ErrInvalidToken
	}
	id, err := strconv.ParseInt(c.Subject, 10, 64)
	if err != nil {
		return Caller{}, ErrInvalidToken
	}
	return Caller{ID: id, Role: c.Role}, nil
}
