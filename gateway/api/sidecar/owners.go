package apisidecar

import (
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

const (
	// maxSidecarOwners bounds the owners of one sidecar.
	maxSidecarOwners = 20
	// maxOwnerEmailBytes is the width of sidecar_owners.email.
	maxOwnerEmailBytes = 255
)

// normalizeOwners trims and lowercases the emails and drops repeats, keeping
// the order, or says why the list cannot be stored. An entry must be a bare
// email: "Ana <ana@example.com>" names a person the match would never find.
func normalizeOwners(owners []string) ([]string, string) {
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range owners {
		email := strings.ToLower(strings.TrimSpace(raw))
		addr, err := mail.ParseAddress(email)
		if err != nil || addr.Address != email {
			return nil, fmt.Sprintf("owner %q is not an email", raw)
		}
		if len(email) > maxOwnerEmailBytes {
			return nil, fmt.Sprintf("owner %q is %d bytes, over %d", raw, len(email), maxOwnerEmailBytes)
		}
		if seen[email] {
			continue
		}
		seen[email] = true
		out = append(out, email)
	}
	if len(out) > maxSidecarOwners {
		return nil, fmt.Sprintf("a sidecar has at most %d owners, got %d", maxSidecarOwners, len(out))
	}
	return out, ""
}

func toOpenAPIOwners(rows []models.SidecarOwner) openapi.SidecarOwners {
	out := openapi.SidecarOwners{Owners: []openapi.SidecarOwner{}}
	for _, r := range rows {
		out.Owners = append(out.Owners, openapi.SidecarOwner{Email: r.Email, Matched: r.Matched})
	}
	return out
}

// GetOwners
//
//	@Summary		Get Sidecar Owners
//	@Description	The people responsible for the sidecar, by email, and whether each email matches one user.
//	@Tags			Sidecars
//	@Produce		json
//	@Param			nameOrID	path		string	true	"Name or UUID of the sidecar"
//	@Success		200			{object}	openapi.SidecarOwners
//	@Failure		404,500		{object}	openapi.HTTPError
//	@Router			/sidecars/{nameOrID}/owners [get]
func GetOwners(c *gin.Context) {
	sidecar := sidecarOfPath(c)
	if sidecar == nil {
		return
	}
	rows, err := models.ListSidecarOwners(models.DB, sidecar.OrgID, sidecar.ID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching sidecar owners")
		return
	}
	c.JSON(http.StatusOK, toOpenAPIOwners(rows))
}

// PutOwners
//
//	@Summary		Set Sidecar Owners
//	@Description	Replace the owners of the sidecar. An empty list removes them all. An owner is an email; the user with that email can sign in later.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			nameOrID		path		string							true	"Name or UUID of the sidecar"
//	@Param			request			body		openapi.SidecarOwnersRequest	true	"The request body resource"
//	@Success		200				{object}	openapi.SidecarOwners
//	@Failure		400,404,422,500	{object}	openapi.HTTPError
//	@Router			/sidecars/{nameOrID}/owners [put]
func PutOwners(c *gin.Context) {
	sidecar := sidecarOfPath(c)
	if sidecar == nil {
		return
	}
	var req openapi.SidecarOwnersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	owners, invalid := normalizeOwners(req.Owners)
	if invalid != "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": invalid})
		return
	}

	// The row lock makes two writers replace the list one after the other.
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		if _, err := models.GetSidecarByNameOrIDForUpdate(tx, sidecar.OrgID, sidecar.ID); err != nil {
			return err
		}
		return models.SetSidecarOwners(tx, sidecar.OrgID, sidecar.ID, owners)
	})
	switch {
	case errors.Is(err, models.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "sidecar not found"})
		return
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed storing sidecar owners")
		return
	}
	saved, err := models.ListSidecarOwners(models.DB, sidecar.OrgID, sidecar.ID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching sidecar owners")
		return
	}
	c.JSON(http.StatusOK, toOpenAPIOwners(saved))
}
