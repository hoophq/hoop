package localauthapi

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/hoophq/hoop/gateway/appconfig"
)

type Claims struct {
	UserEmail            string `json:"email"`
	jwt.RegisteredClaims `json:",inline"`
}

func generateNewAccessToken(subject, email string) (string, error) {
	now := time.Now().UTC()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		UserEmail: email,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("local|%v", subject),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(168 * time.Hour)),
		},
	})
	return token.SignedString(appconfig.Get().JWTSecretKey())
}
