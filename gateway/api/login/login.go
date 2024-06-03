package loginapi

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/apiutils"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/analytics"
	"github.com/runopsio/hoop/gateway/pgrest"
	pglogin "github.com/runopsio/hoop/gateway/pgrest/login"
	pguserauth "github.com/runopsio/hoop/gateway/pgrest/userauth"
	pgusers "github.com/runopsio/hoop/gateway/pgrest/users"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"golang.org/x/oauth2"
)

var errUserInactive = fmt.Errorf("user is inactive")

type handler struct {
	idpProv *idp.Provider
}

func New(provider *idp.Provider) *handler { return &handler{idpProv: provider} }

func (h *handler) Login(c *gin.Context) {
	redirectURL := c.Query("redirect")
	if redirectURL == "" {
		redirectURL = fmt.Sprintf("http://%s/callback", proto.ClientLoginCallbackAddress)
	}
	stateUID := uuid.NewString()
	err := pglogin.New().Upsert(&types.Login{
		ID:       stateUID,
		Redirect: redirectURL,
		Outcome:  "",
		SlackID:  "",
	})
	if err != nil {
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error storing the login"})
		return
	}

	var params = []oauth2.AuthCodeOption{}
	if h.idpProv.Audience != "" {
		params = append(params, oauth2.SetAuthURLParam("audience", h.idpProv.Audience))
	}
	if auth0Params := h.parseAuth0QueryParams(c); len(auth0Params) > 0 {
		params = append(params, auth0Params...)
	}
	url := h.idpProv.AuthCodeURL(stateUID, params...)
	c.JSON(http.StatusOK, map[string]string{"login_url": url})
}

func (h *handler) LoginCallback(c *gin.Context) {
	// https://www.oauth.com/oauth2-servers/authorization/the-authorization-response/
	errorMsg := c.Query("error")
	if errorMsg != "" {
		log.Warnf("login callback error response from identity provider: %v", errorMsg)
		c.JSON(http.StatusUnauthorized, gin.H{"message": "error login callback response from identity provider, contact the administrator"})
		return
	}
	stateUUID := c.Query("state")
	code := c.Query("code")

	log.With("state", stateUUID).Infof("starting callback")
	login, err := pglogin.New().FetchOne(stateUUID)
	if err != nil || login == nil {
		log.With("state", stateUUID).
			Warnf("login record is empty or returned with error, err=%v, isempty=%v", err, login == nil)
		statusCode := http.StatusBadRequest
		if err != nil {
			sentry.CaptureException(err)
			statusCode = http.StatusInternalServerError
		}
		c.JSON(statusCode, gin.H{"message": "failed to retrieve login state internally"})
		return
	}
	// TODO: we should redirect to an ui that will render errors properly
	redirectErrorURL := login.Redirect + "?error=unexpected_error"

	// update the login state when this method returns
	defer updateLoginState(login)
	log.With("state", stateUUID).Debugf("login record found")
	token, uinfo, err := h.verifyIDToken(code)
	if err != nil {
		login.Outcome = fmt.Sprintf("failed verifying id token, reason=%v", err)
		log.Error(login.Outcome)
		c.Redirect(http.StatusTemporaryRedirect, redirectErrorURL)
		return
	}
	subject, err := h.idpProv.VerifyAccessToken(token.AccessToken)
	if err != nil {
		login.Outcome = fmt.Sprintf("failed verifiying access token, reason=%v", err)
		log.Warn(login.Outcome)
		c.Redirect(http.StatusTemporaryRedirect, redirectErrorURL)
		return
	}
	uinfo.Subject = subject
	ctx, err := pguserauth.New().FetchUserContext(subject)
	if err != nil {
		login.Outcome = fmt.Sprintf("failed fetching user subject=%s, email=%s, reason=%v", uinfo.Subject, uinfo.Email, err)
		log.Error(login.Outcome)
		sentry.CaptureException(err)
		c.Redirect(http.StatusTemporaryRedirect, redirectErrorURL)
		return
	}
	redirectSuccessURL := login.Redirect + "?token=" + token.AccessToken

	if !ctx.IsEmpty() && ctx.UserStatus != string(types.UserStatusActive) {
		login.Outcome = fmt.Sprintf("user is not active subject=%s, email=%s", uinfo.Subject, uinfo.Email)
		log.With("org", ctx.OrgID).Warn(login.Outcome)
		c.Redirect(http.StatusTemporaryRedirect, redirectErrorURL)
		return
	}

	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	log.With("sub", uinfo.Subject, "email", uinfo.Email, "profile", uinfo.Profile,
		"multitenant", pgusers.IsOrgMultiTenant(), "ua", userAgent).
		Infof("success login on identity provider")

	// multi tenant won't sync users
	if pgusers.IsOrgMultiTenant() {
		isNewUser, err := registerMultiTenantUser(uinfo, login.SlackID)
		if err != nil {
			login.Outcome = fmt.Sprintf("failed registering multi tenant user subject=%s, email=%s, reason=%v",
				uinfo.Subject, uinfo.Email, err)
			log.With("multitenant", true).Error(login.Outcome)
			sentry.CaptureException(err)
			c.Redirect(http.StatusTemporaryRedirect, redirectErrorURL)
			return
		}
		// the signup process is performed by /api/signup when the gateway is running multi tenant mode
		// track as a login event
		if isNewUser {
			h.analyticsTrack(false, userAgent, ctx)
		}
		login.Outcome = "success"
		c.Redirect(http.StatusTemporaryRedirect, redirectSuccessURL)
		return
	}

	if len(login.SlackID) > 0 {
		ctx.UserSlackID = login.SlackID
	}
	isNewUser, err := syncSingleTenantUser(ctx, uinfo)
	if err != nil {
		login.Outcome = fmt.Sprintf("failed syncing single tenant user subject=%s, email=%s, reason=%v", uinfo.Subject, uinfo.Email, err)
		log.Error(login.Outcome)
		if err != errUserInactive {
			sentry.CaptureException(err)
		}
		c.Redirect(http.StatusTemporaryRedirect, redirectErrorURL)
		return
	}

	h.analyticsTrack(isNewUser, userAgent, ctx)

	// TODO: add analytics (identify / track)
	login.Outcome = "success"
	c.Redirect(http.StatusTemporaryRedirect, redirectSuccessURL)
}

func registerMultiTenantUser(uinfo idp.ProviderUserInfo, slackID string) (isNewUser bool, err error) {
	iuser, err := pgusers.New().FetchUnverifiedUser(&pguserauth.Context{}, uinfo.Email)
	if err != nil {
		return false, fmt.Errorf("failed fetching unverified user, reason=%v", err)
	}
	if iuser == nil {
		return false, nil
	}
	if iuser.Status != string(types.UserStatusActive) {
		log.With("multitenant", true).Warnf("invited user %s is not active", iuser.Email)
		return false, nil
	}
	log.With("multitenant", true).Infof("registering invited user subject=%s, email=%s", uinfo.Subject, iuser.Email)

	iuser.Subject = uinfo.Subject
	iuser.Verified = true
	if len(uinfo.Profile) > 0 {
		iuser.Name = uinfo.Profile
	}
	if len(slackID) > 0 {
		iuser.SlackID = slackID
	}
	if err := pgusers.New().Upsert(*iuser); err != nil {
		return false, fmt.Errorf("failed updating unverified user %s/%s, err=%v", uinfo.Subject, iuser.Email, err)
	}
	return true, nil
}

func syncSingleTenantUser(ctx *pguserauth.Context, uinfo idp.ProviderUserInfo) (isNewUser bool, err error) {
	// if the user exists, sync the groups and the slack id
	userGroups := ctx.UserGroups
	if uinfo.MustSyncGroups {
		userGroups = uinfo.Groups
	}
	if !ctx.IsEmpty() {
		return false, pgusers.New().Upsert(pgrest.User{
			ID:       ctx.UserUUID,
			OrgID:    ctx.OrgID,
			Subject:  ctx.UserSubject,
			Name:     ctx.UserName,
			Email:    ctx.UserEmail,
			Verified: true,
			Status:   ctx.UserStatus,
			SlackID:  ctx.UserSlackID,
			Groups:   userGroups,
		})
	}
	// TODO: check if it's the first user to login and make it admin
	org, totalUsers, err := pgusers.New().FetchOrgByName(proto.DefaultOrgName)
	if err != nil || org == nil || totalUsers == -1 {
		return false, fmt.Errorf("failed fetching default org, users=%v, err=%v", err, totalUsers)
	}
	// first user is admin
	if totalUsers == 0 {
		userGroups = append(userGroups, types.GroupAdmin)
	}

	iuser, err := pgusers.New().FetchUnverifiedUser(&pguserauth.Context{}, uinfo.Email)
	if err != nil {
		return false, fmt.Errorf("failed fetching unverified user, reason=%v", err)
	}
	// validate if an invited user exists and is active and
	// persists as a verified user
	if iuser != nil {
		if iuser.Status != string(types.UserStatusActive) {
			return false, errUserInactive
		}
		log.With("multitenant", false).Infof("registering invited user %s/%s", iuser.Subject, iuser.Email)
		iuser.Subject = uinfo.Subject
		iuser.Verified = true
		if len(ctx.UserName) > 0 {
			iuser.Name = ctx.UserName
		}
		if len(ctx.UserGroups) > 0 {
			iuser.Groups = ctx.UserGroups
		}
		// update it if the login has provided a slack id (slack subscribe flow)
		if len(ctx.UserSlackID) > 0 && len(iuser.SlackID) == 0 {
			iuser.SlackID = ctx.UserSlackID
		}
		if err := pgusers.New().Upsert(*iuser); err != nil {
			return false, fmt.Errorf("failed updating unverified user %s/%s, err=%v", uinfo.Subject, iuser.Email, err)
		}
		return false, nil
	}

	// nutate context for analytics tracking
	ctx.OrgID = org.ID
	ctx.UserSubject = uinfo.Subject
	ctx.UserName = uinfo.Profile
	ctx.UserEmail = uinfo.Email
	ctx.UserGroups = userGroups
	// create a new user in the store
	return true, pgusers.New().Upsert(pgrest.User{
		ID:       uuid.NewString(),
		OrgID:    org.ID,
		Subject:  uinfo.Subject,
		Name:     uinfo.Profile,
		Email:    uinfo.Email,
		Verified: true,
		Status:   string(types.UserStatusActive),
		SlackID:  ctx.UserSlackID,
		Groups:   userGroups,
	})
}

func (h *handler) verifyIDToken(code string) (token *oauth2.Token, uinfo idp.ProviderUserInfo, err error) {
	log.Debugf("verifying access token")
	token, err = h.idpProv.Exchange(h.idpProv.Context, code)
	if err != nil {
		return nil, uinfo, fmt.Errorf("failed exchange authorization code, reason=%v", err)
	}

	idToken, err := h.idpProv.VerifyIDToken(token)
	if err != nil {
		return nil, uinfo, fmt.Errorf("failed veryfing oidc ID Token, reason=%v", err)
	}
	log.With("issuer", idToken.Issuer, "subject", idToken.Subject).
		Infof("token exchanged")

	idTokenClaims := map[string]any{}
	if err := idToken.Claims(&idTokenClaims); err != nil {
		return nil, uinfo, fmt.Errorf("failed extracting id token claims, reason=%v", err)
	}
	debugClaims(idToken.Subject, idTokenClaims, token)
	uinfo = idp.ParseIDTokenClaims(idTokenClaims, h.idpProv.GroupsClaim)
	return
}

func updateLoginState(l *pgrest.Login) {
	loginState := &types.Login{ID: l.ID, Redirect: l.Redirect, Outcome: l.Outcome, SlackID: l.SlackID}
	if err := pglogin.New().Upsert(loginState); err != nil {
		log.Warnf("failed updating login state, reason=%v", err)
	}
}

func debugClaims(subject string, claims map[string]any, accessToken *oauth2.Token) {
	logClaims := []any{}
	for claimKey, claimVal := range claims {
		val := fmt.Sprintf("%v", claimVal)
		if len(val) > 200 {
			logClaims = append(logClaims, claimKey, val[:200]+fmt.Sprintf(" (... %v)", len(val)-200))
			continue
		}
		logClaims = append(logClaims, claimKey, val)
	}
	var isJWT bool
	var jwtHeader []byte
	if parts := strings.Split(accessToken.AccessToken, "."); len(parts) == 3 {
		isJWT = true
		jwtHeader, _ = base64.RawStdEncoding.DecodeString(parts[0])
	}
	log.With(logClaims...).Infof("jwt-access-token=%v, jwt-header=%v, id_token claims=%v, subject=%s, admingroup=%q",
		isJWT, string(jwtHeader),
		len(claims), subject, types.GroupAdmin)
}

// analyticsTrack tracks the user signup/login event
func (h *handler) analyticsTrack(isNewUser bool, userAgent string, ctx *pguserauth.Context) {
	client := analytics.New()
	if !isNewUser {
		client.Track(ctx.UserEmail, analytics.EventLogin, map[string]any{"user-agent": userAgent})
		return
	}
	client.Identify(&types.APIContext{
		OrgID:      ctx.OrgID,
		OrgName:    ctx.OrgName,
		UserID:     ctx.UserEmail, // use user id as email
		UserName:   ctx.UserName,
		UserEmail:  ctx.UserEmail,
		UserGroups: ctx.UserGroups,
		ApiURL:     h.idpProv.ApiURL,
	})
	go func() {
		// wait some time until the identify call get times to reach to intercom
		time.Sleep(time.Second * 10)
		client.Track(ctx.UserEmail, analytics.EventSignup, map[string]any{
			"user-agent": userAgent,
			"name":       ctx.UserName,
			"api-url":    h.idpProv.ApiURL,
		})
	}()
}
