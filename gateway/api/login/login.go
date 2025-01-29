package loginapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/apiutils"
	"github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pgrest"
	pglogin "github.com/hoophq/hoop/gateway/pgrest/login"
	pgorgs "github.com/hoophq/hoop/gateway/pgrest/orgs"
	pguserauth "github.com/hoophq/hoop/gateway/pgrest/userauth"
	"github.com/hoophq/hoop/gateway/security/idp"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"golang.org/x/oauth2"
)

var errUserInactive = fmt.Errorf("user is inactive")

type handler struct {
	idpProv *idp.Provider
}

func New(provider *idp.Provider) *handler { return &handler{idpProv: provider} }

// Login
//
//	@Summary		Login
//	@Description	Returns the login url to perform the signin on the identity provider
//	@Tags			Authentication
//	@Produce		json
//	@Param			redirect		query		string	false	"The URL to redirect after the signin"	Format(string)
//	@Param			screen_hint		query		string	false	"Auth0 specific parameter"				Format(string)
//	@Param			prompt			query		string	false	"The prompt value (OIDC spec)"			Format(string)
//	@Success		200				{object}	openapi.Login
//	@Failure		400,409,422,500	{object}	openapi.HTTPError
//	@Router			/login [get]
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
	c.JSON(http.StatusOK, openapi.Login{URL: url})
}

// LoginCallback
//
//	@Summary				Login Callback
//	@Description.markdown	api-login-callback
//	@Tags					Authentication
//	@Param					error			query		string	false	"The error description in case of failure to authenticate"	Format(string)
//	@Param					state			query		string	false	"The state value (Oauth2)"									Format(string)
//	@Param					code			query		string	false	"The authorization code (Oauth2)"							Format(string)
//	@Success				200				{object}	openapi.Login
//	@Failure				400,409,422,500	{object}	openapi.HTTPError
//	@Router					/callback [get]
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
	// get the user by its email to get the actual subject of that user. This is necessary
	// due to the user subject when it's created inside hoop is changed after that user
	// logs in with the IDP. The email should always come from the IDP as a design of how
	// we handle users in hoop.
	dbUser, err := models.GetUserByEmail(uinfo.Email)
	if err != nil {
		login.Outcome = fmt.Sprintf("failed fetching user by email=%s, reason=%v", uinfo.Email, err)
		log.Error(login.Outcome)
		sentry.CaptureException(err)
		c.Redirect(http.StatusTemporaryRedirect, redirectErrorURL)
		return
	}

	// if the user doesn't exist in the database, we should use the subject from the IDP
	// to allow the user to login. This user will be a new user and will be created at
	// the end of this method.
	if dbUser == nil {
		subject = uinfo.Subject
	} else {
		subject = dbUser.Subject
	}
	ctx, err := pguserauth.New().FetchUserContext(subject)

	if err != nil {
		login.Outcome = fmt.Sprintf("failed fetching user subject=%s, email=%s, reason=%v", uinfo.Subject, uinfo.Email, err)
		log.Error(login.Outcome)
		sentry.CaptureException(err)
		c.Redirect(http.StatusTemporaryRedirect, redirectErrorURL)
		return
	}
	redirectSuccessURL := login.Redirect + "?token=" + token.AccessToken

	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	log.With("sub", uinfo.Subject, "email", uinfo.Email, "profile", uinfo.Profile,
		"multitenant", appconfig.Get().OrgMultitenant(), "ua", userAgent).
		Infof("success login on identity provider")

	// multi tenant won't sync users
	if appconfig.Get().OrgMultitenant() {
		isNewUser := false
		if ctx.UserStatus == string(types.UserStatusInactive) {
			log.With("multitenant", true).Warnf("user %s is inactive. They need to be edited to active before trying to signin", uinfo.Email)
			c.Redirect(http.StatusTemporaryRedirect, redirectErrorURL)
			return
		}
		if ctx.UserStatus != string(types.UserStatusActive) {
			isNewUser, err = registerMultiTenantUser(uinfo, login.SlackID)
			if err != nil {
				login.Outcome = fmt.Sprintf("failed registering multi tenant user subject=%s, email=%s, reason=%v",
					uinfo.Subject, uinfo.Email, err)
				log.With("multitenant", true).Error(login.Outcome)
				sentry.CaptureException(err)
				c.Redirect(http.StatusTemporaryRedirect, redirectErrorURL)
				return
			}
		}

		h.analyticsTrack(isNewUser, userAgent, ctx)
		login.Outcome = "success"
		c.Redirect(http.StatusTemporaryRedirect, redirectSuccessURL)
		return
	}

	if !ctx.IsEmpty() && ctx.UserStatus == string(types.UserStatusInactive) {
		login.Outcome = fmt.Sprintf("user is inactive subject=%s, email=%s", uinfo.Subject, uinfo.Email)
		log.With("org", ctx.OrgID).Warn(login.Outcome)
		c.Redirect(http.StatusTemporaryRedirect, redirectErrorURL)
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
	iuser, err := models.GetInvitedUserByEmail(uinfo.Email)
	if err != nil {
		return false, fmt.Errorf("failed fetching invited user, reason=%v", err)
	}
	// in case the user doesn't exist, we create a new organization
	// and add that user to the new organization
	if iuser == nil {
		newOrgId := uuid.NewString()
		newOrgName := fmt.Sprintf("%s %s", uinfo.Email, "Organization")
		if err := pgorgs.New().CreateOrg(newOrgId, newOrgName, nil); err != nil {
			return false, fmt.Errorf("failed setting new org, reason=%v", err)
		}

		emailVerified := false
		if uinfo.EmailVerified != nil {
			emailVerified = *uinfo.EmailVerified
		}

		userID := uuid.NewString()
		newUser := models.User{
			ID:       userID,
			OrgID:    newOrgId,
			Subject:  uinfo.Subject,
			Name:     uinfo.Profile,
			Email:    uinfo.Email,
			Verified: emailVerified,
			Status:   string(types.UserStatusActive),
			SlackID:  slackID,
		}
		if err := models.CreateUser(newUser); err != nil {
			return false, fmt.Errorf("failed saving new user %s/%s, err=%v", newUser.Subject, newUser.Email, err)
		}
		adminUserGroup := models.UserGroup{
			OrgID:  newOrgId,
			UserID: userID,
			Name:   types.GroupAdmin,
		}
		err = models.InsertUserGroups([]models.UserGroup{adminUserGroup})
		if err != nil {
			return false, fmt.Errorf("failed saving new user group %s/%s, err=%v", newUser.Subject, newUser.Email, err)
		}

		return true, nil
	}
	// This part checks if the user was invited by someone
	// and adds the user to the organization
	if iuser.Status == string(openapi.StatusInvited) {
		iuser.Subject = uinfo.Subject
		iuser.Verified = true
		iuser.Status = string(types.UserStatusActive)
		if len(uinfo.Profile) > 0 {
			iuser.Name = uinfo.Profile
		}
		if len(slackID) > 0 {
			iuser.SlackID = slackID
		}
		if err := models.UpdateUser(iuser); err != nil {
			return false, fmt.Errorf("failed updating invited user %s/%s, err=%v", uinfo.Subject, iuser.Email, err)
		}
		return true, nil
	}
	// If the user is inactive, they can not login in the system
	// until an admin changes their status to active
	if iuser.Status != string(types.UserStatusInactive) {
		log.With("multitenant", true).Warnf("invited user %s is inactive. They need to be edited to active before trying to signin", iuser.Email)
		return false, nil
	}
	return true, nil
}

func syncSingleTenantUser(ctx *pguserauth.Context, uinfo idp.ProviderUserInfo) (isNewUser bool, err error) {
	// if the user exists, sync the groups and the slack id
	userGroups := ctx.UserGroups
	if uinfo.MustSyncGroups {
		userGroups = uinfo.Groups
	}
	// dedupe duplicates from userGroups
	encountered := make(map[string]bool)
	var dedupedUserGroups []string
	for _, ug := range userGroups {
		if !encountered[ug] {
			encountered[ug] = true
			dedupedUserGroups = append(dedupedUserGroups, ug)
		}
	}

	// reassigned the deduped user groups to the user groups to keep compatibility
	userGroups = dedupedUserGroups

	if !ctx.IsEmpty() {
		verified := false
		if uinfo.EmailVerified != nil {
			verified = *uinfo.EmailVerified
		}
		user := models.User{
			ID:    ctx.UserUUID,
			OrgID: ctx.OrgID,
			// always get the subject from the IDP
			Subject:  uinfo.Subject,
			Name:     ctx.UserName,
			Email:    ctx.UserEmail,
			Verified: verified,
			// inactive status verification happens in the upper scope
			// here we change the user status to active in case it's "invited"
			// otherwise, it stays as it is
			Status:  string(types.UserStatusActive),
			SlackID: ctx.UserSlackID,
		}

		newUserGroups := []models.UserGroup{}
		for i := range userGroups {
			newUserGroups = append(newUserGroups, models.UserGroup{
				OrgID:  ctx.OrgID,
				UserID: ctx.UserUUID,
				Name:   userGroups[i],
			})
		}
		if err := models.UpdateUserAndUserGroups(&user, newUserGroups); err != nil {
			return false, fmt.Errorf("failed updating user and user groups %s/%s, err=%v", ctx.UserSubject, ctx.UserEmail, err)
		}

		return false, nil
	}

	org, totalUsers, err := pgorgs.New().FetchOrgByName(proto.DefaultOrgName)
	if err != nil || org == nil || totalUsers == -1 {
		return false, fmt.Errorf("failed fetching default org, users=%v, err=%v", err, totalUsers)
	}
	// first user is admin
	if totalUsers == 0 {
		userGroups = append(userGroups, types.GroupAdmin)
		trackClient := analytics.New()
		// When the first user is created, there's already an
		// anonymous event tracked with his org id. We need to
		// merge this anonymous event with the identified user
		trackClient.Identify(&types.APIContext{
			OrgID:           org.ID,
			OrgName:         org.Name,
			UserName:        uinfo.Profile,
			UserID:          uinfo.Email,
			UserAnonSubject: org.ID,
			UserEmail:       uinfo.Email,
			UserGroups:      userGroups,
			ApiURL:          appconfig.Get().ApiURL(),
		})
		trackClient.Track(uinfo.Email, analytics.EventSingleTenantFirstUserCreated, nil)
	}

	iuser, err := models.GetInvitedUserByEmail(uinfo.Email)

	if err != nil {
		return false, fmt.Errorf("failed fetching invited user, reason=%v", err)
	}
	// validate if an invited user exists and is active and
	// persists as a verified user
	if iuser != nil {
		iuser.Status = string(types.UserStatusActive)
		log.With("multitenant", false).Infof("registering invited user %s/%s", iuser.Subject, iuser.Email)
		iuser.Subject = uinfo.Subject
		iuser.Verified = true
		if len(ctx.UserName) > 0 {
			iuser.Name = ctx.UserName
		}
		// update it if the login has provided a slack id (slack subscribe flow)
		if len(ctx.UserSlackID) > 0 && len(iuser.SlackID) == 0 {
			iuser.SlackID = ctx.UserSlackID
		}

		invitedUserGroups := []models.UserGroup{}
		for i := range userGroups {
			invitedUserGroups = append(invitedUserGroups, models.UserGroup{
				OrgID:  org.ID,
				UserID: iuser.ID,
				Name:   userGroups[i],
			})
		}

		if err := models.UpdateUserAndUserGroups(iuser, invitedUserGroups); err != nil {
			return false, fmt.Errorf("failed updating user and user groups %s/%s, err=%v", ctx.UserSubject, ctx.UserEmail, err)
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
	newUser := models.User{
		ID:       uuid.NewString(),
		OrgID:    org.ID,
		Subject:  uinfo.Subject,
		Name:     uinfo.Profile,
		Email:    uinfo.Email,
		Verified: true,
		Status:   string(types.UserStatusActive),
		SlackID:  ctx.UserSlackID,
	}
	if err := models.CreateUser(newUser); err != nil {
		return false, fmt.Errorf("failed saving new user %s/%s, err=%v", uinfo.Subject, uinfo.Email, err)
	}

	// add the user to the default group
	newUserGroups := []models.UserGroup{}
	for i := range userGroups {
		newUserGroups = append(newUserGroups, models.UserGroup{
			OrgID:  org.ID,
			UserID: newUser.ID,
			Name:   userGroups[i],
		})
	}
	if err := models.InsertUserGroups(newUserGroups); err != nil {
		return false, fmt.Errorf("failed saving new user group %s/%s, err=%v", uinfo.Subject, uinfo.Email, err)
	}

	return true, nil
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
	uinfo = h.idpProv.ParseUserInfo(idTokenClaims, token.AccessToken, h.idpProv.GroupsClaim)
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
	licenseType := license.OSSType
	if ctx.OrgLicenseData != nil && len(*ctx.OrgLicenseData) > 0 {
		var l license.License
		err := json.Unmarshal(*ctx.OrgLicenseData, &l)
		if err == nil {
			licenseType = l.Payload.Type
		}
	}
	client := analytics.New()
	if !isNewUser {
		client.Track(ctx.UserEmail, analytics.EventLogin, map[string]any{
			"auth-method":  appconfig.Get().AuthMethod(),
			"user-agent":   userAgent,
			"license-type": licenseType,
			"name":         ctx.UserName,
			"api-url":      h.idpProv.ApiURL,
		})
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
			"org-id":       ctx.OrgID,
			"auth-method":  appconfig.Get().AuthMethod(),
			"user-agent":   userAgent,
			"license-type": licenseType,
			"name":         ctx.UserName,
			"api-url":      h.idpProv.ApiURL,
		})
	}()
}
