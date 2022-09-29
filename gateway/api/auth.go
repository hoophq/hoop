package api

import (
	"context"
	"github.com/MicahParks/keyfunc"
	"log"
	"time"
)

const jwksURL = "https://runops.us.auth0.com/.well-known/jwks.json"

var jwks *keyfunc.JWKS

func DownloadAuthPublicKey() {
	options := keyfunc.Options{
		Ctx: context.Background(),
		RefreshErrorHandler: func(err error) {
			log.Printf("There was an error with the jwt.Keyfunc\nError: %s", err.Error())
		},
		RefreshInterval:   time.Hour,
		RefreshRateLimit:  time.Minute * 5,
		RefreshTimeout:    time.Second * 10,
		RefreshUnknownKID: true,
	}

	var err error
	jwks, err = keyfunc.Get(jwksURL, options)
	if err != nil {
		log.Fatal("Failed to get auth public key")
	}
}
