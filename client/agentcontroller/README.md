# Agent Controller

The agent controller manages agents in a Kubernetes cluster, this component is available with the `hoop` command line.

| ENV        | REQUIRED | DESCRIPTION |
-------------|----------|-------------|
| KUBECONFIG | no       | Uses a local kubeconfig to interact with a Kubernetes cluster. If it's not provide, it will use the default service account incluster config. |
| JWT_KEY    | yes      | The secret key to validate jwt tokens, must be at least 40 characters. |

> To generate a secret key securely, issue: `openssl rand -base64 32`

## Development Server

To start a development server:

```sh
export KUBECONFIG=
export JWT_KEY=$(openssl rand -base64 32)
echo -n $JWT_KEY > /tmp/agentcontroller.token
make build-dev-client && $HOME/.hoop/bin/hoop start agentcontroller
```

## Token Issuer

In order to generate access tokens, use the snippet below:

```go
package main

import (
	"fmt"
	"log"
	"os"

	jwt "github.com/golang-jwt/jwt/v5"
)

func main() {
	jwtSecretKey, err = os.ReadFile("/tmp/agentcontroller.token")
    if err != nil {
        log.Fatalf("failed reading jwt secret, reason=%v", err)
    }
	iat := time.Now().UTC().Unix()
	exp := time.Now().UTC().Add(time.Hour * 24).Unix()
	j := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iat":  iat,
		"exp":  exp,
	})
	bearerToken, err := j.SignedString(jwtSecretKey)
	if err != nil {
		log.Fatalf("failed token, reason=%v", err)
	}
	fmt.Println(bearerToken)
}
```

## Deployment

- It requires kubectl with context `arn:aws:eks:us-east-2:200074533906:cluster/misc-prod` set
- It requires aws cli and access to secrets manager

```sh
./deploy/run.sh
```