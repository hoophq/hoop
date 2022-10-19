module github.com/runopsio/hoop/gateway

go 1.19

replace github.com/runopsio/hoop/common => ../common

require (
	github.com/MicahParks/keyfunc v1.4.0
	github.com/coreos/go-oidc/v3 v3.4.0
	github.com/gin-contrib/static v0.0.1
	github.com/gin-gonic/gin v1.8.1
	github.com/golang-jwt/jwt/v4 v4.4.2
	github.com/google/uuid v1.3.0
	github.com/runopsio/hoop/common v0.0.0-00010101000000-000000000000
	github.com/tidwall/wal v1.1.7
	golang.org/x/oauth2 v0.0.0-20221006150949-b44042a4b9c1
	google.golang.org/grpc v1.49.0
	olympos.io/encoding/edn v0.0.0-20201019073823-d3554ca0b0a3
)

require (
	github.com/gin-contrib/sse v0.1.0 // indirect
	github.com/go-playground/locales v0.14.0 // indirect
	github.com/go-playground/universal-translator v0.18.0 // indirect
	github.com/go-playground/validator/v10 v10.10.0 // indirect
	github.com/goccy/go-json v0.9.11 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/leodido/go-urn v1.2.1 // indirect
	github.com/mattn/go-isatty v0.0.14 // indirect
	github.com/modern-go/concurrent v0.0.0-20180228061459-e0a39a4cb421 // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/pelletier/go-toml/v2 v2.0.1 // indirect
	github.com/tidwall/gjson v1.10.2 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.0 // indirect
	github.com/tidwall/tinylru v1.1.0 // indirect
	github.com/ugorji/go/codec v1.2.7 // indirect
	golang.org/x/crypto v0.0.0-20220926161630-eccd6366d1be // indirect
	golang.org/x/net v0.0.0-20221004154528-8021a29435af // indirect
	golang.org/x/sys v0.0.0-20221010170243-090e33056c14 // indirect
	golang.org/x/text v0.3.7 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/genproto v0.0.0-20220616135557-88e70c0c3a90 // indirect
	google.golang.org/protobuf v1.28.1 // indirect
	gopkg.in/square/go-jose.v2 v2.6.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
)
