package apiorgs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	pgorgs "github.com/hoophq/hoop/gateway/pgrest/orgs"
	"github.com/hoophq/hoop/gateway/storagev2"
)

func UpdateOrgLicense(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req license.License
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if err := req.Verify(); err != nil {
		log.With("org", ctx.OrgName).Warnf("unable to validate license, reason=%v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": "license is not valid"})
		return
	}
	if err := req.VerifyHost(appconfig.Get().ApiHostname()); err != nil {
		log.With("org", ctx.OrgName).Warn(err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	licenseData, _ := json.Marshal(&req)
	if err := pgorgs.New().UpdateOrgLicense(ctx, licenseData); err != nil {
		msg := fmt.Sprintf("failed updating license, err=%v", err)
		log.With("org", ctx.OrgName).Error(msg)
		c.JSON(http.StatusInternalServerError, gin.H{"message": msg})
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

type SignRequest struct {
	LicenseType  string   `json:"license_type"`
	AllowedHosts []string `json:"allowed_hosts"`
	Description  string   `json:"description"`
	ExpireAt     string   `json:"expire_at"`
}

func SignLicense(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	signerOrgID, signingKey := appconfig.Get().LicenseSigningKey()
	if signingKey == nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "unable to sign license: missing private key"})
		return
	}
	if ctx.OrgID != signerOrgID {
		c.JSON(http.StatusForbidden, gin.H{"message": "unable to sign license: organization not allowed to sign licenses"})
		return
	}
	var req SignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if len(req.AllowedHosts) == 0 || len(req.Description) < 2 || len(req.Description) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"message": "allowed_hosts must contain at least one host and description must be between 2 and 200 characters"})
		return
	}
	expireAt, err := time.ParseDuration(req.ExpireAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid expire_at value, accept (s, m, h)"})
		return
	}
	log.With("user", ctx.UserEmail).Infof("generating new license, type=%v, hosts=%v, description=%v, expire-at=%v",
		req.LicenseType, req.AllowedHosts, req.Description, req.ExpireAt)
	l, err := license.Sign(signingKey, req.LicenseType, req.Description, req.AllowedHosts, expireAt)
	if err != nil {
		log.Warnf("failed sign license, type=%v, hosts=%v, reason=%v", req.LicenseType, req.AllowedHosts, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if err := l.Verify(); err != nil {
		log.Warnf("failed verifying license, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, l)
}
