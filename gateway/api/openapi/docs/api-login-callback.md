Exchanges and validates the authorization code for an access token after being redirect by the external provider.
A success authentication will redirect the user back to the default redirect url provided in the /login route.

In case of error it will include the query string `error=unexpected_error` when redirecting.
