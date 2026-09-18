package apisidecar

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/sidecar/daemon"
	"gorm.io/gorm"
)

// reservedNames would shadow the static routes registered beside
// /sidecars/:nameOrID.
var reservedNames = []string{"handshake", "configuration"}

// licenseIsNotASidecarKey refuses a license authored per sidecar. One
// organization runs under one license, stored on the organization row and
// served to every sidecar by withOrgLicense; a copy in this document would
// be a second answer to the same question, stale the day the org's license
// is renewed. Refusing beats accepting and ignoring: an admin who pastes a
// license here has to learn that it did nothing.
const licenseIsNotASidecarKey = `the "license" key does not belong in a sidecar configuration; ` +
	"the organization's license is served to every sidecar automatically. Set it in Settings -> License"

// configRevision names a served document. It is a hash rather than a counter
// because the plane holds no per-sidecar sequence and derives the document on
// every request: two answers built from the same rows must carry the same
// name, or a sidecar that changed nothing would read as lagging forever.
//
// Opaque by contract. The plane issues it, the sidecar echoes it, and the
// plane compares it to itself; nothing parses it. Truncated to 32 hex
// characters because the column is VARCHAR(64) and this is an equality key,
// not a signature.
func configRevision(served daemon.Config) string {
	doc, err := json.Marshal(served)
	if err != nil {
		// A document that cannot be marshaled is about to fail the response
		// write anyway. An empty revision reads as "unknown" downstream,
		// which is the honest answer and never as "converged".
		return ""
	}
	sum := sha256.Sum256(doc)
	return hex.EncodeToString(sum[:16])
}

// recordHandshake stores what the sidecar reported and what it is being
// served. A failure to record is logged and never fails the handshake: the
// sidecar needs its configuration more than the fleet view needs a row, and
// the next tick is a minute away.
func recordHandshake(sidecarID string, req openapi.SidecarHandshakeRequest, servedRevision string) {
	err := models.RecordSidecarHandshake(models.DB, sidecarID,
		req.Version, req.AppliedRevision, req.LastOutcome, servedRevision)
	if err != nil {
		log.With("sidecar", sidecarID).Warnf("failed recording the sidecar handshake, reason=%v", err)
	}
}

// refuseOverCap answers 422 when a configuration authors more rules than the
// organization's license allows, and reports whether it did.
//
// Every write goes through it. The caps are the sidecar's own
// (sidecar/daemon/limits.go), counted the way the sidecar counts them, so a
// document this accepts is a document a sidecar boots. Refusing here rather
// than only when the document is served is the point: a sidecar that already
// holds an over-cap document keeps its OLD rules and keeps reporting itself
// recently seen, then exits on its next start -- so the whole fleet looks
// healthy until a rescheduling event, and then none of it comes back.
func refuseOverCap(c *gin.Context, orgID string, cfg daemon.Config) bool {
	licenseData, err := models.GetOrgLicenseData(models.DB, orgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed reading the organization license")
		return true
	}
	if err := services.CheckSidecarConfigurationLimits(cfg, licenseData); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return true
	}
	return false
}

// writeSidecarConfiguration commits a configuration edit and refuses one the
// sidecar could not serve.
//
// One transaction around the write and both checks, because both are about the
// document AS STORED: the cap counts what it authors, and the bindings name
// its listeners. Checking either outside the transaction reads a document
// another writer can replace before the write lands, and then reports on one
// nobody has.
//
// The binding check is the one a listener edit needs. Listener names are the
// binding key, so removing, renaming, or re-protocoling a bound lane breaks
// the NEXT handshake -- and it breaks it for the whole sidecar, not for the
// rule: composition answers an error, and every rule on every other lane stops
// being delivered with it. The sidecar keeps its last good document and goes
// on looking healthy while an admin edits rules that no longer reach it.
func writeSidecarConfiguration(db *gorm.DB, licenseData json.RawMessage, write func(tx *gorm.DB) (*models.Sidecar, error)) (*models.Sidecar, error) {
	var item *models.Sidecar
	err := db.Transaction(func(tx *gorm.DB) error {
		sc, err := write(tx)
		if err != nil {
			return err
		}
		if err := services.CheckSidecarConfigurationLimits(daemon.Config(sc.Configuration), licenseData); err != nil {
			return err
		}
		if err := services.ValidateSidecarBindingsForConfiguration(tx, sc); err != nil {
			return err
		}
		item = sc
		return nil
	})
	return item, err
}

// answerSidecarWrite maps what writeSidecarConfiguration refused onto a status.
// An over-cap document and a broken binding are the admin's to fix and read
// 422; a check that could not run is ours and reads 500.
func answerSidecarWrite(c *gin.Context, err error) {
	var overCap services.ErrSidecarConfigOverCap
	var broken services.ErrSidecarBindingBroken
	switch {
	case errors.Is(err, models.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "sidecar not found"})
	case errors.As(err, &overCap):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": overCap.Error()})
	case errors.As(err, &broken):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": broken.Error()})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed writing sidecar configuration")
	}
}

// licenseManagedHeader tells a sidecar that this gateway owns the licensing
// decision, so an absent license in the answer means the organization holds
// none rather than "this gateway does not know about the feature".
//
// A HEADER, not a field in the document: the sidecar decodes that body with
// DisallowUnknownFields, so a new key would be a startup failure on every
// build that predates it. The distinction matters because a sidecar can be
// upgraded before the gateway it talks to. Without this, an upgraded sidecar
// against an older gateway would read silence as an unlicensed organization
// and discard the operator's own license, which for a config that needs the
// paid caps is not a downgrade but a refusal to start.
//
// The name comes from the module that parses it, so a rename fails a build
// instead of disabling the signal.
const licenseManagedHeader = daemon.LicenseManagedHeader

// Create Sidecar
//
//	@Summary		Create Sidecar
//	@Description	Register a sidecar. The token is returned only once in this response and cannot be recovered.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			request				body		openapi.SidecarRequest	true	"The request body resource"
//	@Success		201					{object}	openapi.SidecarCreateResponse
//	@Failure		400,409,422,500		{object}	openapi.HTTPError
//	@Router			/sidecars [post]
func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.SidecarRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if err := apivalidation.ValidateResourceName(req.Name); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	if slices.Contains(reservedNames, req.Name) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "name \"" + req.Name + "\" is reserved"})
		return
	}

	cfg, err := services.ParseSidecarConfiguration(req.Configuration)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	if cfg.License != "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": licenseIsNotASidecarKey})
		return
	}
	if refuseOverCap(c, ctx.OrgID, cfg) {
		return
	}

	rawKey, err := services.GenerateSidecarKey()
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating sidecar")
		return
	}
	sidecar := &models.Sidecar{
		OrgID:         ctx.OrgID,
		Name:          req.Name,
		KeyHash:       models.HashAPIKey(rawKey),
		Configuration: models.SidecarConfiguration(cfg),
		CreatedBy:     ctx.UserEmail,
	}

	switch err := models.CreateSidecar(models.DB, sidecar); {
	case err == nil:
		c.JSON(http.StatusCreated, openapi.SidecarCreateResponse{
			SidecarResponse: toResponse(*sidecar),
			Token:           rawKey,
		})
	case errors.Is(err, models.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"message": "a sidecar with this name already exists"})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating sidecar")
	}
}

// List Sidecars
//
//	@Summary		List Sidecars
//	@Description	List all sidecars for the organization
//	@Tags			Sidecars
//	@Produce		json
//	@Success		200	{array}		openapi.SidecarResponse
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/sidecars [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	items, err := models.ListSidecars(models.DB, ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing sidecars")
		return
	}
	bindings := bindingsBySidecar(ctx.OrgID, "")
	result := []openapi.SidecarResponse{}
	for _, item := range items {
		resp := toResponse(item)
		resp.BoundRules = bindings[item.ID]
		result = append(result, resp)
	}
	c.JSON(http.StatusOK, result)
}

// bindingsBySidecar groups the org's rule bindings by sidecar id, for one
// sidecar when id is set and for every one otherwise.
//
// A failure returns nothing rather than an error: the bindings are a read-side
// annotation on a page whose subject is the sidecar, and failing the whole
// request because an annotation could not be built would take the page down
// over a decoration. The listener then renders without its chips, which is what
// it did before this field existed.
func bindingsBySidecar(orgID, sidecarID string) map[string][]openapi.SidecarRuleBinding {
	out := map[string][]openapi.SidecarRuleBinding{}
	org, err := uuid.Parse(orgID)
	if err != nil {
		return out
	}
	rows, err := models.ListSidecarRuleBindings(models.DB, org, sidecarID)
	if err != nil {
		log.Warnf("failed listing the rules bound to the organization's sidecars, err=%v", err)
		return out
	}
	for _, r := range rows {
		out[r.SidecarID] = append(out[r.SidecarID], openapi.SidecarRuleBinding{
			Kind: r.Kind, RuleName: r.RuleName, ListenerName: r.ListenerName,
		})
	}
	return out
}

// Get Sidecar
//
//	@Summary		Get Sidecar
//	@Description	Get a sidecar by name or ID
//	@Tags			Sidecars
//	@Produce		json
//	@Param			nameOrID	path		string	true	"Name or UUID of the sidecar"
//	@Success		200			{object}	openapi.SidecarResponse
//	@Failure		404,500		{object}	openapi.HTTPError
//	@Router			/sidecars/{nameOrID} [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	item, err := models.GetSidecarByNameOrID(models.DB, ctx.OrgID, c.Param("nameOrID"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "sidecar not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching sidecar")
		return
	}
	resp := toResponse(*item)
	resp.BoundRules = bindingsBySidecar(ctx.OrgID, item.ID)[item.ID]
	c.JSON(http.StatusOK, resp)
}

// Delete Sidecar
//
//	@Summary		Delete Sidecar
//	@Description	Delete a sidecar. The token stops working immediately.
//	@Tags			Sidecars
//	@Produce		json
//	@Param			nameOrID	path	string	true	"Name or UUID of the sidecar"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/sidecars/{nameOrID} [delete]
func Delete(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	_, err := models.DeleteSidecarByNameOrID(models.DB, ctx.OrgID, c.Param("nameOrID"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "sidecar not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed deleting sidecar")
		return
	}
	c.Writer.WriteHeader(http.StatusNoContent)
}

// Update Sidecar
//
//	@Summary		Update Sidecar
//	@Description	Replace the configuration a sidecar serves. The sidecar picks it up on its next heartbeat.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			nameOrID			path		string							true	"Name or UUID of the sidecar"
//	@Param			request				body		openapi.SidecarUpdateRequest	true	"The request body resource"
//	@Success		200					{object}	openapi.SidecarResponse
//	@Failure		400,404,422,500		{object}	openapi.HTTPError
//	@Router			/sidecars/{nameOrID} [put]
func Put(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.SidecarUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	cfg, err := services.ParseSidecarConfiguration(req.Configuration)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	if cfg.License != "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": licenseIsNotASidecarKey})
		return
	}
	licenseData, err := models.GetOrgLicenseData(models.DB, ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed reading the organization license")
		return
	}
	item, err := writeSidecarConfiguration(models.DB, licenseData, func(tx *gorm.DB) (*models.Sidecar, error) {
		return models.UpdateSidecarConfiguration(tx, ctx.OrgID,
			c.Param("nameOrID"), models.SidecarConfiguration(cfg))
	})
	if err != nil {
		answerSidecarWrite(c, err)
		return
	}
	c.JSON(http.StatusOK, toResponse(*item))
}

// Patch Sidecar Configuration
//
//	@Summary		Patch Sidecar Configuration
//	@Description	Merge a partial configuration into the document a sidecar serves: the keys sent are updated and the rest are left as stored. Unlike PUT it never replaces the whole document, so it cannot overwrite a configuration a sidecar imported meanwhile. load_from_disk false clears the key, handing the document back to the control plane.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			nameOrID			path		string							true	"Name or UUID of the sidecar"
//	@Param			request				body		openapi.SidecarPatchRequest		true	"The request body resource"
//	@Success		200					{object}	openapi.SidecarResponse
//	@Failure		400,404,422,500		{object}	openapi.HTTPError
//	@Router			/sidecars/{nameOrID} [patch]
func Patch(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.SidecarPatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	merge, removeLoadFromDisk, err := services.ParseSidecarConfigurationPatch(req.Configuration)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	licenseData, err := models.GetOrgLicenseData(models.DB, ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed reading the organization license")
		return
	}
	// A patch is a partial document: what it authors in total is only known
	// once the merge has run. So the merge and the checks share one
	// transaction and a refusal rolls the merge back, rather than the gateway
	// reading the stored document first and racing another writer between the
	// read and the write.
	item, err := writeSidecarConfiguration(models.DB, licenseData, func(tx *gorm.DB) (*models.Sidecar, error) {
		return models.PatchSidecarConfiguration(tx, ctx.OrgID, c.Param("nameOrID"), merge, removeLoadFromDisk)
	})
	if err != nil {
		answerSidecarWrite(c, err)
		return
	}
	c.JSON(http.StatusOK, toResponse(*item))
}

// Sidecar Handshake
//
//	@Summary		Sidecar Handshake
//	@Description	Authenticated with the hoop-sidecar-token header. Records the reported version and returns the configuration the sidecar must serve. A sidecar whose stored configuration sets load_from_disk receives only that flag and its license, and runs its own config file. Answers 412 while no configuration with listeners is assigned, recording nothing: a sidecar that cannot run must not show up as recently seen. The answer carries the organization's license in its "license" key; the sidecar verifies that signature itself and the license is never stored per sidecar.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			hoop-sidecar-token	header		string							true	"The token returned when the sidecar was created"
//	@Param			request				body		openapi.SidecarHandshakeRequest	true	"The request body resource"
//	@Success		200				{object}	map[string]interface{}
//	@Header			200				{string}	hoop-sidecar-license-managed	"Present when this gateway owns the licensing decision, so an answer with no license means the organization holds none. A gateway older than the feature omits it, and the sidecar then keeps its own license sources."
//	@Failure		400,401,412,500	{object}	openapi.HTTPError
//	@Router			/sidecars/handshake [post]
func Handshake(c *gin.Context) {
	sidecar := apiroutes.SidecarFromContext(c)
	if sidecar == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return
	}
	var req openapi.SidecarHandshakeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if sidecar.Configuration.LoadFromDisk != nil && *sidecar.Configuration.LoadFromDisk {
		licenseData, err := models.GetOrgLicenseData(models.DB, sidecar.OrgID)
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed reading the organization license")
			return
		}
		// Running fine on its own file, so it is recently seen. The 412
		// below is for a sidecar that cannot run at all.
		//
		// No revision: the plane does not own this sidecar's document, so it
		// has nothing to be converged with. The state renders from
		// load_from_disk instead.
		recordHandshake(sidecar.ID, req, "")
		c.Header(licenseManagedHeader, "true")
		c.JSON(http.StatusOK, diskModeConfig{LoadFromDisk: true, License: string(licenseData)})
		return
	}
	// An empty answer would only kill the caller: the sidecar refuses to
	// serve a config with no listeners and exits. The 412 lets it import
	// its local file instead, and skipping recordHandshake keeps a process
	// that cannot run from showing up as recently seen.
	if len(sidecar.Configuration.Listeners) == 0 {
		c.JSON(http.StatusPreconditionFailed, gin.H{"message": "no configuration is assigned to this sidecar; " +
			"start the sidecar with its config file to import it, or author the configuration in the control plane"})
		return
	}
	served, err := withOrgLicense(sidecar)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed reading the organization license")
		return
	}
	revision := configRevision(served)
	recordHandshake(sidecar.ID, req, revision)
	c.Header(licenseManagedHeader, "true")
	c.Header(daemon.ConfigRevisionHeader, revision)
	c.JSON(http.StatusOK, served)
}

// withOrgLicense answers the config a sidecar must serve, carrying the
// organization's license.
//
// The license is NOT stored per sidecar. It lives in one place, the
// organization row, and is written into the served document here, on the way
// out. The sidecar then verifies that signature itself (daemon.Config's
// UseLicense), so nothing is trusted because of who sent it.
//
// It rides in the document's existing "license" key rather than beside it.
// The sidecar decodes this body with DisallowUnknownFields, so a sibling key
// would be refused by every build that does not know it yet.
//
// The document goes out as it was stored, unverified: PUT /orgs/license
// already checked the signature and the allowed hosts against this API's
// hostname, and a sidecar's hostname is a scheduler-generated pod name that
// no license can name. An expired document still goes out, because the
// sidecar has its own rule for a term that ended and cannot apply it to a
// license it never received.
func withOrgLicense(sc *models.Sidecar) (daemon.Config, error) {
	licenseData, err := models.GetOrgLicenseData(models.DB, sc.OrgID)
	if err != nil {
		// Not found is not a missing license, it is a missing org: the
		// token authenticated against a row that names it.
		return daemon.Config(sc.Configuration), err
	}
	// The rules an admin bound to this sidecar are folded in here, on the way
	// out, and never stored: the row keeps what an admin authored and the
	// answer is derived on every handshake. Composition touches only the
	// sections the sidecar hot-reloads, so a rule edit reaches a running
	// process without restarting it.
	composed, err := services.ComposeSidecarConfiguration(models.DB, sc)
	if err != nil {
		return daemon.Config(sc.Configuration), err
	}
	// The free tier caps rules PER PROCESS, and it counts what the served
	// document authors -- so bound rules push the count up even though the
	// stored configuration passed the same check when it was written.
	//
	// This is the backstop, and it exists because of how the sidecar fails
	// without it: a document over the cap is refused while the process lives
	// (it keeps the rules it has) and a HARD EXIT on its next boot. The fleet
	// keeps serving stale rules, keeps reporting itself recently seen, and
	// then every pod that reschedules -- a helm upgrade, a node drain --
	// crash-loops at once, hours after the save looked fine.
	//
	// Answering the handshake with an error instead is the one failure the
	// sidecar survives: fetchControlPlaneConfig logs it and keeps running.
	if err := services.CheckSidecarConfigurationLimits(composed, licenseData); err != nil {
		return daemon.Config(sc.Configuration), err
	}
	return servedConfig(models.SidecarConfiguration(composed), licenseData), nil
}

// servedConfig is the document itself: the stored configuration with the
// license written in. A copy, never the stored value: the caller holds the
// row the middleware loaded, and a sidecar's row must not grow a license
// because something read it.
func servedConfig(cfg models.SidecarConfiguration, licenseData json.RawMessage) daemon.Config {
	served := daemon.Config(cfg)
	// Assigned unconditionally, so an organization with no license serves
	// none. A row written before this feature can carry a `license` of its
	// own -- the write routes only started refusing one here -- and letting
	// that through would license a fleet the organization has unlicensed.
	served.License = string(licenseData)
	// A false (or set) load_from_disk is stripped: this answer is the
	// control-plane-owned document, never the instruction. An older
	// sidecar decodes with DisallowUnknownFields and would reject the
	// whole config over a key it does not declare, and could then never
	// recover.
	served.LoadFromDisk = nil
	return served
}

// Import Sidecar Configuration
//
//	@Summary		Import Sidecar Configuration
//	@Description	Authenticated with the hoop-sidecar-token header. Stores the config document a sidecar carried locally, once: the import is refused with 409 when the control plane already holds a configuration with listeners, or when the sidecar loads its configuration from disk.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			hoop-sidecar-token	header		string	true	"The token returned when the sidecar was created"
//	@Param			request				body		object	true	"The configuration document, the same shape the handshake answers"
//	@Success		200					{object}	map[string]interface{}
//	@Failure		400,401,409,422,500	{object}	openapi.HTTPError
//	@Router			/sidecars/configuration [put]
func ImportConfiguration(c *gin.Context) {
	sidecar := apiroutes.SidecarFromContext(c)
	if sidecar == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return
	}
	raw, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	cfg, err := services.ParseSidecarConfiguration(raw)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	if len(cfg.Listeners) == 0 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "an imported configuration must declare at least one listener"})
		return
	}
	// Dropped rather than refused, unlike the admin routes above. A
	// standalone sidecar legitimately names a license in its config file,
	// and the connect journey must not fail over a key this row will never
	// hold. The sidecar strips it before pushing; this is the second
	// barrier, for a client that does not.
	cfg.License = ""
	// Checked on the way in, against the ORGANIZATION's license rather than
	// whatever the sidecar was running under. A file that booted on a
	// sidecar with its own license can be over the cap for the org that
	// adopts it, and storing it would make the plane serve a document the
	// sidecar then refuses on its next start. The sidecar renders this 422
	// as "the control plane refused the imported config", and keeps running
	// its local file meanwhile.
	if refuseOverCap(c, sidecar.OrgID, cfg) {
		return
	}
	// An imported configuration is plane-owned by definition: the sidecar
	// pushed its file to hand ownership over, so the row records the flag
	// explicitly off.
	defaultLoadFromDisk := false
	cfg.LoadFromDisk = &defaultLoadFromDisk
	item, err := models.AdoptSidecarConfiguration(models.DB, sidecar.OrgID, sidecar.ID, models.SidecarConfiguration(cfg))
	switch {
	case err == nil:
		c.JSON(http.StatusOK, item.Configuration)
	case errors.Is(err, models.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"message": "the control plane already holds a configuration for this sidecar; edit it there"})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed importing sidecar configuration")
	}
}

// Sidecar Configuration
//
//	@Summary		Sidecar Configuration
//	@Description	Authenticated with the hoop-sidecar-token header. Returns the configuration the sidecar must serve, carrying the organization's license in its "license" key, or only the load_from_disk flag and the license when the sidecar loads its configuration from disk. Unlike the handshake it records nothing, so a poll never overwrites what the sidecar last reported about itself.
//	@Tags			Sidecars
//	@Produce		json
//	@Param			hoop-sidecar-token	header		string	true	"The token returned when the sidecar was created"
//	@Success		200		{object}	map[string]interface{}
//	@Header			200		{string}	hoop-sidecar-license-managed	"Present when this gateway owns the licensing decision; see the handshake."
//	@Failure		401,500	{object}	openapi.HTTPError
//	@Router			/sidecars/configuration [get]
func Configuration(c *gin.Context) {
	sidecar := apiroutes.SidecarFromContext(c)
	if sidecar == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return
	}
	if sidecar.Configuration.LoadFromDisk != nil && *sidecar.Configuration.LoadFromDisk {
		licenseData, err := models.GetOrgLicenseData(models.DB, sidecar.OrgID)
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed reading the organization license")
			return
		}
		c.Header(licenseManagedHeader, "true")
		c.JSON(http.StatusOK, diskModeConfig{LoadFromDisk: true, License: string(licenseData)})
		return
	}
	served, err := withOrgLicense(sidecar)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed reading the organization license")
		return
	}
	c.Header(licenseManagedHeader, "true")
	c.JSON(http.StatusOK, served)
}

// diskModeConfig is the whole answer a released sidecar receives: the
// instruction to run its own file, and the license to run it under. Nothing
// that could configure a lane, and no zero-valued config fields -- an answer
// naming a null listener list or an empty audit block would read as a
// configuration rather than an instruction.
type diskModeConfig struct {
	LoadFromDisk bool   `json:"load_from_disk"`
	License      string `json:"license,omitempty"`
}

func toResponse(s models.Sidecar) openapi.SidecarResponse {
	resp := openapi.SidecarResponse{
		ID:            s.ID,
		OrgID:         s.OrgID,
		Name:          s.Name,
		CreatedBy:     s.CreatedBy,
		CreatedAt:     s.CreatedAt,
		Configuration: daemon.Config(s.Configuration),
	}
	// A row written before this feature can carry one, and Put refuses it:
	// handing it back would 422 an admin for a field they never authored
	// and cannot see the purpose of. The license belongs to the
	// organization, so it is not part of this document in either direction.
	resp.Configuration.License = ""
	resp.LastSeenAt = s.LastSeenAt
	// Each stays empty when the column is NULL, so a sidecar that has never
	// handshaken, and one too old to report, both read as unknown. Rendering
	// a zero value as a real answer here would report convergence nobody
	// claimed.
	resp.Version = derefOrEmpty(s.ReportedVersion)
	resp.ServedRevision = derefOrEmpty(s.ServedRevision)
	resp.AppliedRevision = derefOrEmpty(s.AppliedRevision)
	resp.LastOutcome = derefOrEmpty(s.LastOutcome)
	return resp
}

func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
