Hoop implements Oauth2 and OIDC protocol to authenticate users in the system. To obtain a valid access token users need to authenticate in their own identity provider which is generated as a JSON response to the endpoint `http(s)://{{ .Host }}{{ .BasePath }}/login`. The identity provider them redirects the user to the callback endpoint containing the access token.

The recommended approach of obtaining an access token is by visiting the Webapp main's page (https://{{ .Host }}) or using the **Hoop command line**. Example:

```sh
hoop config create --api-url https://{{ .Host }}
# save the token after authenticating at $HOME/.hoop/config.toml
hoop login
# show token information
hoop config view --raw
```

With an access token you could use any HTTP client to interact with the documented endpoints.
The token must be sent through the `Authorization` header.

Example:

```sh
# obtain the current configuration of the server
curl https://{{ .Host }}{{ .BasePath }}/serverinfo -H "Authorization: Bearer $ACCESS_TOKEN"
```
