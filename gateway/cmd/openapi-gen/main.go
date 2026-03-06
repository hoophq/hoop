package main

import (
	"os"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
)

func main() {
	v3Spec, err := openapi.GenerateV3Spec()
	if err != nil {
		log.Fatal(err)
	}

	err = os.WriteFile("gateway/api/openapi/openapiv3.json", v3Spec, 0644)
	if err != nil {
		log.Fatal(err)
	}
}
