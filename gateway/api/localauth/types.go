package localauthapi

import "github.com/golang-jwt/jwt/v5"

type Claims struct {
	UserID    string `json:"user_id"`
	UserEmail string `json:"user_email"`
	jwt.RegisteredClaims
}
