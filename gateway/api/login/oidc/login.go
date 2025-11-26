package loginoidcapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/aws/smithy-go/ptr"
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
	"github.com/hoophq/hoop/gateway/idp"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"golang.org/x/oauth2"
)

var errUserInactive = fmt.Errorf("user is inactive")

type handler struct {
	apiURL string
}

func New() *handler {
	return &handler{apiURL: appconfig.Get().ApiURL()}
}

func (h *handler) loadOidcVerifier(c *gin.Context) (idp.OidcVerifier, bool) {
	oidcVerifier, err := idp.NewOidcVerifierProvider()
	switch err {
	case idp.ErrUnknownIdpProvider:
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "OIDC provider not configured"})
	case nil:
	default:
		log.Errorf("failed to load IDP provider: %v", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "internal server error, failed loading IDP provider"})
	}
	return oidcVerifier, err == nil
}

// Login
//
//	@Summary		OIDC | Login
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
	oidc, ok := h.loadOidcVerifier(c)
	if !ok {
		return
	}

	redirectURL, err := parseRedirectURL(c)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	stateUID := uuid.NewString()
	err = models.CreateLogin(&models.Login{
		ID:        stateUID,
		Redirect:  redirectURL,
		Outcome:   "",
		SlackID:   "",
		UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		log.Errorf("internal error storing the login, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error storing the login"})
		return
	}

	var params = []oauth2.AuthCodeOption{}
	if audience := oidc.GetAudience(); audience != "" {
		params = append(params, oauth2.SetAuthURLParam("audience", audience))
	}
	if auth0Params := h.parseAuth0QueryParams(c); len(auth0Params) > 0 {
		params = append(params, auth0Params...)
	}
	url := oidc.GetAuthCodeURL(stateUID, params...)
	c.JSON(http.StatusOK, openapi.Login{URL: url})
}

// LoginCallback
//
//	@Summary				OIDC | Login Callback
//	@Description.markdown	api-login-callback
//	@Tags					Authentication
//	@Param					error			query								string	false	"The error description in case of failure to authenticate"																				Format(string)
//	@Param					state			query								string	false	"The state value (Oauth2)"																												Format(string)
//	@Param					code			query								string	false	"The authorization code (Oauth2)"																										Format(string)
//	@Param					Location		header								string	false	"The location header to redirect in case of failure or success. In case of error it will contain the `error=<message>` query string"	Format(string)
//	@Success				307				"Redirect with Success or Error"	{string}
//	@Failure				400,409,422,500	{object}							openapi.HTTPError
//	@Router					/callback [get]
func (h *handler) LoginCallback(c *gin.Context) {
	oidc, ok := h.loadOidcVerifier(c)
	if !ok {
		return
	}

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
	login, err := models.GetLoginByState(stateUUID)
	if err != nil {
		log.With("state", stateUUID).
			Warnf("login record is empty or returned with error, err=%v, isempty=%v", err, login == nil)
		statusCode := http.StatusBadRequest
		if err != models.ErrNotFound {
			statusCode = http.StatusInternalServerError
		}
		c.JSON(statusCode, gin.H{"message": "failed to retrieve login state internally"})
		return
	}
	// TODO: we should redirect to an ui that will render errors properly
	redirectErrorURL := login.Redirect + "?error=unexpected_error"

	// update the login state when this method returns
	defer updateLoginState(login)
	log.With("state", stateUUID).Debugf("login record found, verifying ID Token")
	token, uinfo, err := oidc.VerifyIDTokenForCode(code)
	if err != nil {
		login.Outcome = fmt.Sprintf("failed verifying ID Token, reason=%v", err)
		log.Error(login.Outcome)
		c.Redirect(http.StatusTemporaryRedirect, redirectErrorURL)
		return
	}

	log.With("subject", uinfo.Subject, "email", uinfo.Email, "email-verified", uinfo.EmailVerified).
		Infof("obtained user information, sync-groups=%v, sync-gsuite=%v, groups=%v, fetch-gsuite-err=%v",
			uinfo.MustSyncGroups, uinfo.MustSyncGsuiteGroups, len(uinfo.Groups), err != nil)

	subject, err := oidc.VerifyAccessToken(token.AccessToken)
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
	ctx, err := models.GetUserContext(subject)
	if err != nil {
		login.Outcome = fmt.Sprintf("failed fetching user subject=%s, email=%s, reason=%v", uinfo.Subject, uinfo.Email, err)
		log.Error(login.Outcome)
		sentry.CaptureException(err)
		c.Redirect(http.StatusTemporaryRedirect, redirectErrorURL)
		return
	}

	err = models.UpsertUserToken(models.DB, subject, token.AccessToken)
	if err != nil {
		login.Outcome = fmt.Sprintf("failed upserting user token subject=%s, email=%s, reason=%v", uinfo.Subject, uinfo.Email, err)
		log.Error(login.Outcome)
		sentry.CaptureException(err)
		c.Redirect(http.StatusTemporaryRedirect, redirectErrorURL)
		return
	}

	redirectSuccessURL := login.Redirect + "?token=" + token.AccessToken

	url, _ := url.Parse(login.Redirect)
	if url != nil && url.Host != proto.ClientLoginCallbackAddress {
		redirectSuccessURL = login.Redirect
		c.SetCookie(
			"hoop_access_token",
			token.AccessToken,
			0,
			"/",
			"",
			true,
			false,
		)
	}

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

func registerMultiTenantUser(uinfo idptypes.ProviderUserInfo, slackID string) (isNewUser bool, err error) {
	iuser, err := models.GetInvitedUserByEmail(uinfo.Email)
	if err != nil {
		return false, fmt.Errorf("failed fetching invited user, reason=%v", err)
	}
	// in case the user doesn't exist, we create a new organization
	// and add that user to the new organization
	if iuser == nil {
		newOrgName := fmt.Sprintf("%s %s", uinfo.Email, "Organization")
		org, err := models.CreateOrganization(newOrgName, nil)
		if err != nil {
			return false, fmt.Errorf("failed creating organization, reason=%v", err)
		}

		emailVerified := false
		if uinfo.EmailVerified != nil {
			emailVerified = *uinfo.EmailVerified
		}

		userID := uuid.NewString()
		newUser := models.User{
			ID:       userID,
			OrgID:    org.ID,
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
			OrgID:  org.ID,
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

func syncSingleTenantUser(ctx *models.Context, uinfo idptypes.ProviderUserInfo) (isNewUser bool, err error) {
	// if the user exists, sync the groups and the slack id
	userGroups := ctx.UserGroups
	if uinfo.MustSyncGroups {
		userGroups = uinfo.Groups

		if !ctx.IsEmpty() && ctx.IsAdmin() {
			userGroups = append(userGroups, types.GroupAdmin)
		}
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
			ID:    ctx.UserID,
			OrgID: ctx.OrgID,
			// always get the subject from the IDP
			Subject:  uinfo.Subject,
			Name:     ctx.UserName,
			Email:    ctx.UserEmail,
			Verified: verified,
			// inactive status verification happens in the upper scope
			// here we change the user status to active in case it's "invited"
			// otherwise, it stays as it is
			Status:         string(types.UserStatusActive),
			SlackID:        ctx.UserSlackID,
			HashedPassword: ptr.ToString(ctx.UserHashedPassword),
		}

		newUserGroups := []models.UserGroup{}
		for i := range userGroups {
			newUserGroups = append(newUserGroups, models.UserGroup{
				OrgID:  ctx.OrgID,
				UserID: ctx.UserID,
				Name:   userGroups[i],
			})
		}
		if err := models.UpdateUserAndUserGroups(&user, newUserGroups); err != nil {
			return false, fmt.Errorf("failed updating user and user groups %s/%s, err=%v", ctx.UserSubject, ctx.UserEmail, err)
		}

		return false, nil
	}

	orgList, err := models.ListAllOrganizations()
	if err != nil || len(orgList) == 0 {
		return false, fmt.Errorf("failed fetching default organization, err=%v", err)
	}

	org, err := models.GetOrganizationByNameOrID(proto.DefaultOrgName)
	if err != nil {
		return false, fmt.Errorf("failed fetching default org, err=%v", err)
	}

	isFirstUserInOrg := org.TotalUsers == 0

	// first user is admin
	if isFirstUserInOrg {
		userGroups = append(userGroups, types.GroupAdmin)
	}

	// mutate context for analytics tracking
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

	if isFirstUserInOrg {
		trackClient := analytics.New()
		// When the first user is created, there's already an
		// anonymous event tracked with his org id. We need to
		// merge this anonymous event with the identified user
		trackClient.Identify(&types.APIContext{
			OrgID:           org.ID,
			UserID:          newUser.Subject,
			UserAnonSubject: org.ID,
		})
		trackClient.Track(newUser.Subject, analytics.EventSingleTenantFirstUserCreated, nil)
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

func updateLoginState(login *models.Login) {
	if err := models.UpdateLoginOutcome(login.ID, login.Outcome); err != nil {
		log.Warnf("failed updating login state, reason=%v", err)
	}
}

// analyticsTrack tracks the user signup/login event
func (h *handler) analyticsTrack(isNewUser bool, userAgent string, ctx *models.Context) {
	licenseType := license.OSSType
	if len(ctx.OrgLicenseData) > 0 {
		var l license.License
		err := json.Unmarshal(ctx.OrgLicenseData, &l)
		if err == nil {
			licenseType = l.Payload.Type
		}
	}
	client := analytics.New()
	if !isNewUser {
		client.Track(ctx.UserID, analytics.EventLogin, map[string]any{
			"org-id":       ctx.OrgID,
			"auth-method":  appconfig.Get().AuthMethod(),
			"user-agent":   userAgent,
			"license-type": licenseType,
		})
		return
	}
	client.Identify(&types.APIContext{
		OrgID:  ctx.OrgID,
		UserID: ctx.UserID,
	})
	go func() {
		// wait some time until the identify call get times to reach to intercom
		time.Sleep(time.Second * 10)
		client.Track(ctx.UserID, analytics.EventSignup, map[string]any{
			"org-id":       ctx.OrgID,
			"auth-method":  appconfig.Get().AuthMethod(),
			"user-agent":   userAgent,
			"license-type": licenseType,
		})
	}()
}

// parseRedirectURL validates the redirect query attribute to match against the API_URL env
// or the default localhost address
func parseRedirectURL(c *gin.Context) (string, error) {
	redirectURL := c.Query("redirect")
	if redirectURL != "" {
		u, _ := url.Parse(redirectURL)
		if u == nil || u.Hostname() != appconfig.Get().ApiHostname() {
			return "", fmt.Errorf("redirect attribute does not match with api url")
		}
		return redirectURL, nil
	}
	return fmt.Sprintf("http://%s/callback", proto.ClientLoginCallbackAddress), nil
}
