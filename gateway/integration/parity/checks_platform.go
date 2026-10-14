//go:build integration && parity

package parity

import "net/http"

var allLiveRuns = map[Run]Expect{Gateway: Must, ControlPlaneAlias: Must}

var _ = register(Check{
	ID:    "PLT-01",
	Title: "serverinfo reports the gateway mode and a valid Enterprise license",
	Runs:  allLiveRuns,
	Fn: func(c *C) {
		var info struct {
			ApplicationMode string `json:"application_mode"`
			LicenseInfo     struct {
				IsValid bool   `json:"is_valid"`
				Type    string `json:"type"`
			} `json:"license_info"`
		}
		c.Admin().Do(c, http.MethodGet, "/serverinfo", nil).Expect(c, http.StatusOK).JSON(c, &info)
		if info.ApplicationMode != "gateway" {
			c.Fatalf("application_mode=%q, want gateway", info.ApplicationMode)
		}
		if !info.LicenseInfo.IsValid || info.LicenseInfo.Type != "enterprise" {
			c.Fatalf("license is_valid=%v type=%q, want a valid enterprise license", info.LicenseInfo.IsValid, info.LicenseInfo.Type)
		}
	},
})

var _ = register(Check{
	ID:    "PLT-02",
	Title: "local login issues a token for the registered admin",
	Runs:  allLiveRuns,
	Fn: func(c *C) {
		r := c.Anonymous().Do(c, http.MethodPost, "/localauth/login",
			map[string]string{"email": adminEmail, "password": adminPassword}).Expect(c, http.StatusOK)
		if r.Header.Get("Token") == "" {
			c.Fatalf("login answered 200 with no Token header")
		}
		c.Anonymous().Do(c, http.MethodPost, "/localauth/login",
			map[string]string{"email": adminEmail, "password": "wrong-password"}).Expect(c, http.StatusUnauthorized)
	},
})

var _ = register(Check{
	ID:    "PLT-03",
	Title: "an unauthenticated call to a protected route is refused",
	Runs:  allLiveRuns,
	Fn: func(c *C) {
		c.Anonymous().Do(c, http.MethodGet, "/connections", nil).Expect(c, http.StatusUnauthorized)
	},
})
