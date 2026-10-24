package apisidecar

import (
	"crypto/rand"
	"errors"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

// A device-code login (RFC 8628 in shape, not in wire format). The sidecar
// makes its own token, sends it once with a requested name, and shows a short
// code. An admin who opens the code in the web app approves it into their
// organization, which creates a sidecar holding that token's hash. The
// sidecar polls until then and connects with the token it already has, so no
// secret crosses the plane after the first request and none is stored.
const (
	sidecarLoginTTL      = 10 * time.Minute
	sidecarLoginInterval = 5 * time.Second
	// A decided login is kept this long, so a sidecar that polls late still
	// reads its answer instead of "expired".
	sidecarLoginKeep = time.Hour
	// The start route needs no credential, so the table is bounded as a
	// whole. Past this many live codes, new logins are refused until some
	// expire.
	maxPendingSidecarLogins = 10000
	// minSidecarTokenLen is the shortest token accepted: "hsc_" and the
	// base64 of 32 random bytes is 47 characters.
	minSidecarTokenLen = 40
)

// userCodeAlphabet has no vowels and no characters people misread (0/O, 1/I),
// so a code never spells a word and survives being read aloud.
const userCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"

func newUserCode() (string, error) {
	var b strings.Builder
	for i := 0; i < 8; i++ {
		if i == 4 {
			b.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(userCodeAlphabet))))
		if err != nil {
			return "", err
		}
		b.WriteByte(userCodeAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// normalizeUserCode accepts a code typed with any case, spaces or no dash.
func normalizeUserCode(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(raw) {
		if strings.ContainsRune(userCodeAlphabet, r) {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) != 8 {
		return ""
	}
	return s[:4] + "-" + s[4:]
}

// StartLogin
//
//	@Summary		Start a sidecar login
//	@Description	Starts a device-code login. Send the token the sidecar made for itself in the hoop-sidecar-token header; only its hash is stored. Show the user code and the verification URL, then poll /sidecars/login/poll.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			request	body		openapi.SidecarLoginRequest	true	"The request body resource"
//	@Success		201		{object}	openapi.SidecarLoginResponse
//	@Failure		400,409,422	{object}	openapi.HTTPError
//	@Failure		429,503		{object}	openapi.HTTPError
//	@Router			/sidecars/login [post]
func StartLogin(c *gin.Context) {
	if !loginStartLimiter.allow(loginStartKey) {
		c.JSON(http.StatusTooManyRequests, gin.H{"message": "too many sidecar logins right now; wait a minute and try again"})
		return
	}
	token := c.GetHeader(apiroutes.SidecarTokenHeader)
	if !strings.HasPrefix(token, "hsc_") || len(token) < minSidecarTokenLen {
		c.JSON(http.StatusBadRequest, gin.H{"message": "send a sidecar token made with at least 32 random bytes in the " +
			apiroutes.SidecarTokenHeader + " header"})
		return
	}
	var req openapi.SidecarLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if msg := checkSidecarName(req.Name); msg != "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": msg})
		return
	}

	now := time.Now().UTC()
	if err := models.DeleteExpiredSidecarLogins(models.DB, now, sidecarLoginKeep); err != nil {
		log.Warnf("failed pruning expired sidecar logins, err=%v", err)
	}
	pending, err := models.CountPendingSidecarLogins(models.DB, now)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed starting the sidecar login")
		return
	}
	if pending >= maxPendingSidecarLogins {
		c.JSON(http.StatusServiceUnavailable, gin.H{"message": "too many sidecar logins are waiting; try again in a few minutes"})
		return
	}

	login := &models.SidecarLogin{
		KeyHash:       models.HashAPIKey(token),
		RequestedName: req.Name,
		ExpiresAt:     now.Add(sidecarLoginTTL),
	}
	// The same token sent twice would make two codes approving into one key
	// hash. Checked before the insert, so a conflict there means a user code.
	switch _, err := models.GetSidecarLoginByKeyHash(models.DB, login.KeyHash); {
	case err == nil:
		c.JSON(http.StatusConflict, gin.H{"message": "this token already started a login; make a new token"})
		return
	case !errors.Is(err, gorm.ErrRecordNotFound):
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed starting the sidecar login")
		return
	}
	// A token a sidecar already holds would fail at approve, on the unique
	// key hash, long after the person started.
	switch _, err := models.GetSidecarByKeyHash(models.DB, login.KeyHash); {
	case err == nil:
		c.JSON(http.StatusConflict, gin.H{"message": "a sidecar already uses this token; make a new token"})
		return
	case !errors.Is(err, models.ErrNotFound):
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed starting the sidecar login")
		return
	}
	// A taken user code is a collision in 20^8 codes: draw again.
	for attempt := 0; ; attempt++ {
		if login.UserCode, err = newUserCode(); err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed starting the sidecar login")
			return
		}
		err = models.CreateSidecarLogin(models.DB, login)
		if err == nil {
			break
		}
		if errors.Is(err, models.ErrAlreadyExists) && attempt < 3 {
			continue
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed starting the sidecar login")
		return
	}

	c.JSON(http.StatusCreated, openapi.SidecarLoginResponse{
		UserCode:        login.UserCode,
		VerificationURL: verificationURL(login.UserCode),
		ExpiresIn:       int(sidecarLoginTTL.Seconds()),
		Interval:        int(sidecarLoginInterval.Seconds()),
	})
}

// verificationURL is the web app page that approves the code.
func verificationURL(userCode string) string {
	return webAppURL("/sidecars/login?code=" + url.QueryEscape(userCode))
}

// webAppURL is a web app page. The web app is served under the API URL's
// path, as the session links in reviews are.
func webAppURL(path string) string {
	return strings.TrimRight(appconfig.Get().FullApiURL(), "/") + path
}

// PollLogin
//
//	@Summary		Poll a sidecar login
//	@Description	Answers 200 once an admin approved the login, 428 while it waits, 403 when denied and 410 when it expired or is unknown. Send the same hoop-sidecar-token header as the start.
//	@Tags			Sidecars
//	@Produce		json
//	@Success		200			{object}	openapi.SidecarLoginStatus
//	@Failure		403,410,428	{object}	openapi.SidecarLoginStatus
//	@Router			/sidecars/login/poll [post]
func PollLogin(c *gin.Context) {
	token := c.GetHeader(apiroutes.SidecarTokenHeader)
	if token == "" {
		c.JSON(http.StatusGone, openapi.SidecarLoginStatus{Status: "expired"})
		return
	}
	login, err := models.GetSidecarLoginByKeyHash(models.DB, models.HashAPIKey(token))
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusGone, openapi.SidecarLoginStatus{Status: "expired"})
		return
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed reading the sidecar login")
		return
	}
	switch {
	case login.Status == models.SidecarLoginApproved:
		status := openapi.SidecarLoginStatus{Status: models.SidecarLoginApproved, Name: login.RequestedName}
		if login.SidecarID != nil && login.OrgID != nil {
			if sc, err := models.GetSidecarByNameOrID(models.DB, *login.OrgID, *login.SidecarID); err == nil {
				status.Name = sc.Name
			}
			status.URL = webAppURL("/sidecars/" + url.PathEscape(*login.SidecarID))
		}
		c.JSON(http.StatusOK, status)
	case login.Status == models.SidecarLoginDenied:
		c.JSON(http.StatusForbidden, openapi.SidecarLoginStatus{Status: models.SidecarLoginDenied})
	case login.Expired(time.Now().UTC()):
		c.JSON(http.StatusGone, openapi.SidecarLoginStatus{Status: "expired"})
	default:
		c.JSON(http.StatusPreconditionRequired, openapi.SidecarLoginStatus{Status: models.SidecarLoginPending})
	}
}

// GetLogin
//
//	@Summary		Get a sidecar login
//	@Description	The pending login an admin is about to approve. A code that expired or was decided answers 410 and says nothing about it.
//	@Tags			Sidecars
//	@Produce		json
//	@Param			code	path		string	true	"The user code"
//	@Success		200		{object}	openapi.SidecarLoginDetails
//	@Failure		404,410,429	{object}	openapi.HTTPError
//	@Router			/sidecars/login/{code} [get]
func GetLogin(c *gin.Context) {
	if tooManyCodeRequests(c, storagev2.ParseContext(c).UserID) {
		return
	}
	code := normalizeUserCode(c.Param("code"))
	if code == "" {
		answerCodeMiss(c, http.StatusNotFound, "no sidecar login has this code")
		return
	}
	login, err := models.GetSidecarLoginByUserCode(models.DB, code)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		answerCodeMiss(c, http.StatusNotFound, "no sidecar login has this code")
		return
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed reading the sidecar login")
		return
	}
	// Only the pending request is shown. A decided one is another tenant's
	// business: its name and outcome would leak to whoever guessed the code.
	if login.Status != models.SidecarLoginPending || login.Expired(time.Now().UTC()) {
		answerCodeMiss(c, http.StatusGone, errLoginGone.Error())
		return
	}
	c.JSON(http.StatusOK, openapi.SidecarLoginDetails{
		UserCode:  login.UserCode,
		Name:      login.RequestedName,
		Status:    login.Status,
		CreatedAt: login.CreatedAt,
		ExpiresAt: login.ExpiresAt,
	})
}

// errLoginGone ends a read, an approval or a denial of a code that is no
// longer pending. One message for expired and decided, so a guessed code
// tells nothing about another tenant's login.
var errLoginGone = errors.New("this sidecar login code expired or was already used; start the sidecar login again")

// lockPendingLogin reads the login under a row lock and refuses one that is
// no longer pending.
func lockPendingLogin(tx *gorm.DB, code string) (*models.SidecarLogin, error) {
	login, err := models.GetSidecarLoginByUserCodeForUpdate(tx, code)
	if err != nil {
		return nil, err
	}
	if login.Status != models.SidecarLoginPending || login.Expired(time.Now().UTC()) {
		return nil, errLoginGone
	}
	return login, nil
}

// tooManyCodeRequests answers 429 when this user has spent its budget of
// code reads and decisions.
func tooManyCodeRequests(c *gin.Context, userID string) bool {
	userOK := loginCodeLimiter.allow(userID)
	// A spent plane budget refuses only users that guessed wrong lately.
	guessing := loginMissLimiter.exhausted(loginMissKey) && loginMissers.any(userID)
	if userOK && !guessing {
		return false
	}
	c.JSON(http.StatusTooManyRequests, gin.H{"message": "too many sidecar code requests; wait a minute and try again"})
	return true
}

// answerCodeMiss answers a code that matches no pending login, and spends
// from the plane's budget of wrong codes: guessing is bounded across every
// account and address, not only per caller.
func answerCodeMiss(c *gin.Context, status int, message string) {
	loginMissLimiter.allow(loginMissKey)
	loginMissers.mark(storagev2.ParseContext(c).UserID)
	c.JSON(status, gin.H{"message": message})
}

// ApproveLogin
//
//	@Summary		Approve a sidecar login
//	@Description	Creates a sidecar in the caller's organization that the waiting sidecar's token reaches. A free organization holds 1 sidecar.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			code		path		string								true	"The user code"
//	@Param			request		body		openapi.SidecarLoginApproveRequest	false	"The request body resource"
//	@Success		201			{object}	openapi.SidecarResponse
//	@Failure		400,403,404	{object}	openapi.HTTPError
//	@Failure		409,410,422,429	{object}	openapi.HTTPError
//	@Router			/sidecars/login/{code}/approve [post]
func ApproveLogin(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	if tooManyCodeRequests(c, ctx.UserID) {
		return
	}
	code := normalizeUserCode(c.Param("code"))
	if code == "" {
		answerCodeMiss(c, http.StatusNotFound, "no sidecar login has this code")
		return
	}
	var req openapi.SidecarLoginApproveRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
			return
		}
	}

	var sidecar *models.Sidecar
	var badName string
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		login, err := lockPendingLogin(tx, code)
		if err != nil {
			return err
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = login.RequestedName
		}
		if badName = checkSidecarName(name); badName != "" {
			return errBadName
		}
		sidecar = &models.Sidecar{
			OrgID:     ctx.OrgID,
			Name:      name,
			KeyHash:   login.KeyHash,
			CreatedBy: ctx.UserEmail,
		}
		if err := services.CreateSidecarWithinSeats(tx, sidecar); err != nil {
			return err
		}
		return models.DecideSidecarLogin(tx, login.ID, models.SidecarLoginApproved, ctx.OrgID, sidecar.ID, ctx.UserEmail)
	})
	switch {
	case err == nil:
		c.JSON(http.StatusCreated, toResponse(*sidecar))
	case errors.Is(err, errBadName):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": badName})
	default:
		answerLoginDecision(c, err)
	}
}

// DenyLogin
//
//	@Summary		Deny a sidecar login
//	@Description	Refuses the login; the waiting sidecar stops.
//	@Tags			Sidecars
//	@Param			code	path	string	true	"The user code"
//	@Success		204
//	@Failure		404,410,429	{object}	openapi.HTTPError
//	@Router			/sidecars/login/{code}/deny [post]
func DenyLogin(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	if tooManyCodeRequests(c, ctx.UserID) {
		return
	}
	code := normalizeUserCode(c.Param("code"))
	if code == "" {
		answerCodeMiss(c, http.StatusNotFound, "no sidecar login has this code")
		return
	}
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		login, err := lockPendingLogin(tx, code)
		if err != nil {
			return err
		}
		return models.DecideSidecarLogin(tx, login.ID, models.SidecarLoginDenied, "", "", ctx.UserEmail)
	})
	if err != nil {
		answerLoginDecision(c, err)
		return
	}
	c.Status(http.StatusNoContent)
	c.Writer.WriteHeaderNow()
}

var errBadName = errors.New("invalid sidecar name")

// checkSidecarName applies the rules Post applies to a name, as a message.
func checkSidecarName(name string) string {
	if err := apivalidation.ValidateResourceName(name); err != nil {
		return err.Error()
	}
	if services.IsReservedSidecarName(name) {
		return "name \"" + name + "\" is reserved"
	}
	return ""
}

func answerLoginDecision(c *gin.Context, err error) {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		answerCodeMiss(c, http.StatusNotFound, "no sidecar login has this code")
	case errors.Is(err, errLoginGone):
		answerCodeMiss(c, http.StatusGone, err.Error())
	case errors.Is(err, services.ErrSidecarSeatLimit):
		c.JSON(http.StatusForbidden, gin.H{"message": err.Error()})
	case errors.Is(err, models.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"message": "a sidecar with this name already exists; choose another name"})
	default:
		answerSidecarWrite(c, err)
	}
}
