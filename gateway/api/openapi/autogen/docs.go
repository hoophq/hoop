// Package autogen Code generated by swaggo/swag. DO NOT EDIT
package autogen

import "github.com/swaggo/swag"

const docTemplate = `{
    "schemes": {{ marshal .Schemes }},
    "swagger": "2.0",
    "info": {
        "description": "{{escape .Description}}",
        "title": "{{.Title}}",
        "termsOfService": "https://hoop.dev/docs/legal/tos",
        "contact": {
            "name": "Help",
            "url": "https://help.hoop.dev",
            "email": "help@hoop.dev"
        },
        "license": {
            "name": "MIT",
            "url": "https://opensource.org/license/mit"
        },
        "version": "{{.Version}}"
    },
    "host": "{{.Host}}",
    "basePath": "{{.BasePath}}",
    "paths": {
        "/agents": {
            "post": {
                "description": "Createn an agent key",
                "consumes": [
                    "application/json"
                ],
                "produces": [
                    "application/json"
                ],
                "tags": [
                    "agents"
                ],
                "summary": "Create an agent",
                "responses": {
                    "201": {
                        "description": "Created",
                        "schema": {
                            "$ref": "#/definitions/openapi.AgentCreateResponse"
                        }
                    }
                }
            }
        }
    },
    "definitions": {
        "openapi.AgentCreateResponse": {
            "type": "object",
            "properties": {
                "token": {
                    "type": "string",
                    "example": "grpcs://default:xagt-zKQQA9PAjCVJ4O8VlE2QZScNEbfmFisg_OerkI21NEg@127.0.0.1:8010?mode=standard"
                }
            }
        }
    },
    "securityDefinitions": {
        "OAuth2Implicit": {
            "type": "oauth2",
            "flow": "implicit",
            "authorizationUrl": "https://login.microsoftonline.com/d60ba6f0-ad5f-4917-aa19-f8d4241f8bc7/oauth2/v2.0/authorize",
            "scopes": {
                "email": "",
                "openid": "",
                "profile": ""
            }
        }
    }
}`

// SwaggerInfo holds exported Swagger Info so clients can modify it
var SwaggerInfo = &swag.Spec{
	Version:          "1.0",
	Host:             "",
	BasePath:         "",
	Schemes:          []string{"http", "https"},
	Title:            "Hoop Api",
	Description:      "Hoop.dev is an access gateway for databases and servers with an API for packet manipulation",
	InfoInstanceName: "swagger",
	SwaggerTemplate:  docTemplate,
	LeftDelim:        "{{",
	RightDelim:       "}}",
}

func init() {
	swag.Register(SwaggerInfo.InstanceName(), SwaggerInfo)
}
