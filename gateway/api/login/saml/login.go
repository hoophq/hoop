package loginsamlapi

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/idp"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	saml2 "github.com/russellhaering/gosaml2"
)

type handler struct {
	apiURL string
}

func New() *handler {
	return &handler{apiURL: appconfig.Get().ApiURL()}
}

func (h *handler) loadSamlVerifier(c *gin.Context) (idp.SamlVerifier, bool) {
	samlVerifier, err := idp.NewSamlVerifierProvider()
	switch err {
	case idp.ErrUnknownIdpProvider:
		c.JSON(http.StatusUnauthorized, gin.H{"message": "SAML provider not configured"})
	case nil:
	default:
		log.Errorf("failed to load IDP provider: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal server error, failed loading SAML provider"})
	}
	return samlVerifier, err == nil
}

// SamlLogin
//
//	@Summary		SAML | Login
//	@Description	Returns the login url to perform the signin on the identity provider.
//	@Tags			Authentication
//	@Produce		json
//	@Param			redirect	query		string	false	"The URL to redirect after the signin"	Format(string)
//	@Success		200			{object}	openapi.Login
//	@Failure		400,401,500	{object}	openapi.HTTPError
//	@Router			/saml/login [get]
func (h *handler) SamlLogin(c *gin.Context) {
	saml, ok := h.loadSamlVerifier(c)
	if !ok {
		return
	}

	doc, err := saml.ServiceProvider().BuildAuthRequestDocument()
	if err != nil {
		log.Errorf("failed to build SAML auth request document, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to build SAML auth request document"})
		return
	}

	var requestID string
	root := doc.Root()
	if root != nil {
		idAttr := root.SelectAttr("ID")
		if idAttr != nil {
			requestID = idAttr.Value
		}
	}

	if requestID == "" {
		log.Warnf("SAML request ID is empty, cannot proceed with login")
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error, SAML request ID is empty"})
		return
	}
	if requestID[0] == '_' {
		requestID = requestID[1:]
	}
	redirectURL, err := parseRedirectURL(c)
	if err != nil {
		log.Warnf("failed to parse redirect URL, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error, failed to parse redirect URL"})
		return
	}

	err = models.CreateLogin(&models.Login{
		ID:        requestID,
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

	authURL, err := saml.ServiceProvider().BuildAuthURLFromDocument("", doc)
	if err != nil {
		log.Errorf("failed to build SAML auth URL: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to generate SAML auth URL"})
		return
	}
	log.With("state", requestID).Infof("initiate SAML login")
	c.JSON(http.StatusOK, openapi.Login{URL: authURL})
}

// LoginCallback
//
//	@Summary		SAML | Login Callback
//	@Description	It redirects the user to the redirect URL with a JWT access token on success. A success authentication will redirect the user back to the default redirect url provided in the /saml/login route. In case of error it will include the query string error=<description> when redirecting.
//	@Tags			Authentication
//	@Param			error			query								string	false	"The error description in case of failure to authenticate"																				Format(string)
//	@Param			Location		header								string	false	"The location header to redirect in case of failure or success. In case of error it will contain the `error=<message>` query string"	Format(string)
//	@Success		307				"Redirect with Success or Error"	{string}
//	@Failure		400,409,422,500	{object}							openapi.HTTPError
//	@Router			/saml/callback [post]
func (h *handler) SamlLoginCallback(c *gin.Context) {
	saml, ok := h.loadSamlVerifier(c)
	if !ok {
		return
	}

	// parse saml information from identity provider
	err := c.Request.ParseForm()
	if err != nil {
		log.Warnf("SAML login callback error, reason=%v", err)
		c.JSON(http.StatusUnauthorized, gin.H{"message": "failed to parse form data"})
		return
	}

	encSamlResponse := c.Request.FormValue("SAMLResponse")
	assertionInfo, err := saml.ServiceProvider().RetrieveAssertionInfo(encSamlResponse)
	if err != nil {
		log.Warnf("SAML parse assertion error, reason=%v", err)
		c.JSON(http.StatusUnauthorized, gin.H{"message": "failed to parse SAML assertion"})
		return
	}

	// the library doesn't accept multiple assertions
	// it's safe to assume that the first assertion is the one we need
	var notOnOrAfter time.Time
	for _, ass := range assertionInfo.Assertions {
		if ass.Conditions != nil {
			notOnOrAfter, _ = time.Parse(time.RFC3339, ass.Conditions.NotOnOrAfter)
			break
		}
	}

	var responseID string
	resp, _ := saml.ServiceProvider().ValidateEncodedResponse(encSamlResponse)
	if resp != nil {
		responseID = resp.InResponseTo
	}

	if responseID == "" {
		log.Warnf("SAML response ID is empty, cannot proceed with login")
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error, SAML response ID is empty"})
		return
	}
	if responseID[0] == '_' {
		responseID = responseID[1:]
	}

	if assertionInfo.WarningInfo.InvalidTime {
		log.Warnf("SAML assertion warning: invalid time")
		c.JSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return
	}

	if assertionInfo.WarningInfo.NotInAudience {
		log.Warnf("SAML assertion warning: not in audience")
		c.JSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return
	}

	// validate login state in the database
	log := log.With("state", responseID)
	log.Infof("starting login callback")

	// Obtaining the request id from the login state ensures that
	// it only validate callback requests from the gateway server
	//
	// It prevents and disables IDP initiated flows for security reasons
	login, err := models.GetLoginByState(responseID)
	switch err {
	case models.ErrNotFound:
		log.Warnf("SAML login not found for state %s", responseID)
		c.JSON(http.StatusForbidden, gin.H{"message": "invalid login state"})
		return
	case nil:
	default:
		log.Warnf("failed to get login by state, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error, failed to get login by state"})
		return
	}

	// update the login state when this method returns
	defer updateLoginState(login)

	// parse and validate user information in the database
	uinfo := parseToUserInfo(saml, *assertionInfo)
	log = log.With("email", uinfo.Email)
	usr, err := models.GetUserByEmailV2(uinfo.Email)
	switch err {
	case models.ErrNotFound:
	case nil:
	default:
		log.Errorf("failed obtaining user, reason=%v", err)
		redirectToErrURL(c, login.Redirect, "unable to obtain user from database")
		return
	}
	if usr == nil {
		org, err := models.GetOrganizationByNameOrID(proto.DefaultOrgName)
		if err != nil {
			log.Errorf("unable to obtain default organization, reason=%v", err)
			redirectToErrURL(c, login.Redirect, "unable to obtain default organization")
			return
		}
		// first user is admin
		if org.TotalUsers == 0 && !slices.Contains(uinfo.Groups, types.GroupAdmin) {
			uinfo.Groups = append(uinfo.Groups, types.GroupAdmin)
		}
		usr = &models.UserV2{
			ID:             uuid.NewString(),
			OrgID:          org.ID,
			Subject:        uinfo.Subject,
			Email:          uinfo.Email,
			Name:           "",
			Picture:        nil,
			Verified:       true,
			Status:         string(openapi.StatusActive),
			SlackID:        nil,
			HashedPassword: nil,
		}
	}

	switch openapi.StatusType(usr.Status) {
	case openapi.StatusInvited:
		usr.Status = string(openapi.StatusActive)
	case openapi.StatusInactive:
		redirectToErrURL(c, login.Redirect, "user is inactive")
		return
	}

	// sync attributes
	usr.Name = uinfo.Profile
	if uinfo.MustSyncGroups {
		usr.Groups = uinfo.Groups
	}

	var slackID *string
	if login.SlackID != "" {
		slackID = &login.SlackID
	}
	usr.SlackID = slackID

	if err := models.UpsertUserV2(usr); err != nil {
		log.Errorf("failed saving user state, reason=%v", err)
		redirectToErrURL(c, login.Redirect, "unable to save user state")
		return
	}

	// generate a new access token for the user base on the saml assertion
	log.Infof("obtained user information, sync-groups=%v (%v), notOnOrAfter=%v",
		uinfo.MustSyncGroups, len(uinfo.Groups), notOnOrAfter.Format(time.RFC3339))

	if notOnOrAfter.IsZero() {
		log.Warnf("unable to parse SAML assertion notOnOrAfter, using default duration of 60 minutes")
		notOnOrAfter = time.Now().Add(time.Minute * 60).UTC()
	}
	tokenDuration := notOnOrAfter.Sub(time.Now().UTC())
	sessionToken, err := saml.NewAccessToken(uinfo.Subject, uinfo.Email, tokenDuration)
	if err != nil {
		log.Errorf("failed generating access token, reason=%v", err)
		redirectToErrURL(c, login.Redirect, "unable to generate access token")
		return
	}

	err = models.UpsertUserToken(models.DB, uinfo.Subject, sessionToken)
	if err != nil {
		log.Errorf("failed upserting user token, reason=%v", err)
		redirectToErrURL(c, login.Redirect, "unable to store user token")
		return
	}

	redirectSuccessURL := fmt.Sprintf("%s?token=%v", login.Redirect, sessionToken)
	url, _ := url.Parse(login.Redirect)
	if url != nil && url.Host != proto.ClientLoginCallbackAddress && !appconfig.Get().ForceUrlTokenExchange() {
		redirectSuccessURL = login.Redirect

		http.SetCookie(c.Writer, &http.Cookie{
			Name:     "hoop_access_token",
			Value:    sessionToken,
			Path:     "/",
			MaxAge:   0,
			HttpOnly: false,
			Secure:   true,
			SameSite: http.SameSiteNoneMode,
		})
	}
	c.Redirect(http.StatusTemporaryRedirect, redirectSuccessURL)
}

func parseToUserInfo(saml idp.SamlVerifier, assertionInfo saml2.AssertionInfo) (uinfo idptypes.ProviderUserInfo) {
	uinfo = idptypes.ProviderUserInfo{
		Subject: assertionInfo.NameID,
		Email:   assertionInfo.NameID,
	}
	var firstName string
	var lastName string

	debugAssertions(assertionInfo)
	for key, val := range assertionInfo.Values {

		switch key {
		case saml.ServiceProvider().GroupsClaim:
			uinfo.MustSyncGroups = true
			groups := map[string]string{}
			for _, v := range val.Values {
				groups[v.Value] = v.Value
			}
			for group := range groups {
				uinfo.Groups = append(uinfo.Groups, group)
			}
			sort.Strings(uinfo.Groups)
		case "http://schemas.microsoft.com/identity/claims/displayname":
			if len(val.Values) > 0 {
				firstName = val.Values[0].Value
			}
		case "first_name", "name":
			if len(val.Values) > 0 {
				firstName = val.Values[0].Value
			}
		case "last_name":
			if len(val.Values) > 0 {
				lastName = val.Values[0].Value
			}
		}
	}
	uinfo.Profile = firstName
	if firstName != "" && lastName != "" {
		uinfo.Profile = fmt.Sprintf("%s %s", firstName, lastName)
	}
	return uinfo
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

func redirectToErrURL(c *gin.Context, loginURL string, format string, a ...any) {
	redirectURL := fmt.Sprintf("%s?error=%s", loginURL,
		url.QueryEscape(fmt.Errorf(format, a...).Error()))
	c.Redirect(http.StatusTemporaryRedirect, redirectURL)
}

func updateLoginState(login *models.Login) {
	if err := models.UpdateLoginOutcome(login.ID, login.Outcome); err != nil {
		log.Warnf("failed updating login state, reason=%v", err)
	}
}

func debugAssertions(ass saml2.AssertionInfo) {
	logClaims := []any{}
	for key, attr := range ass.Values {
		parts := strings.Split(key, "/")
		if len(parts) > 0 {
			key = parts[len(parts)-1]
		}
		for _, val := range attr.Values {
			val := fmt.Sprintf("%v", val.Value)
			logClaims = append(logClaims, key, val)
		}
	}
	log.With(logClaims...).Debugf("saml assertions, email=%s, admingroup=%q",
		ass.NameID, types.GroupAdmin)
}
