package agentcontroller

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

func parseToken(r *http.Request) (string, error) {
	tokenHeader := r.Header.Get("Authorization")
	tokenParts := strings.Split(tokenHeader, " ")
	if len(tokenParts) != 2 || tokenParts[0] != "Bearer:" || tokenParts[1] == "" {
		return "", errors.New("invalid authorization header")
	}
	return strings.TrimSpace(tokenParts[1]), nil
}

func isValidateToken(jwtSecretKey string, w http.ResponseWriter, r *http.Request) bool {
	requestToken, err := parseToken(r)
	if err != nil {
		httpError(w, http.StatusUnauthorized, "failed to extract token from authorization header")
		return false
	}
	_, err = jwt.Parse(requestToken, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(jwtSecretKey), nil
	},
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuedAt(),
	)
	if err != nil {
		httpError(w, http.StatusUnauthorized, "is not a valid token")
		return false
	}
	return true
}

type authMiddleware struct {
	jwtSecretKey string
}

func NewAuthMiddleware(jwtSecretKey string) *authMiddleware {
	return &authMiddleware{jwtSecretKey: jwtSecretKey}
}

func (m *authMiddleware) Handler(next http.HandlerFunc) func(http.ResponseWriter, *http.Request) {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isValidateToken(m.jwtSecretKey, w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}
