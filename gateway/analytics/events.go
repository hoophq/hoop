package analytics

const (
	// default org
	EventDefaultOrgCreated = "hoop-default-org-created"

	// auth
	EventLogin  = "hoop-login"
	EventSignup = "hoop-signup"

	// connections
	EventCreateConnection = "hoop-create-connection"
	EventUpdateConnection = "hoop-update-connection"
	EventDeleteConnection = "hoop-delete-connection"

	// users
	EventUpdateUser           = "hoop-update-user"
	EventCreateInvitedUser    = "hoop-create-invited-user"
	EventCreateServiceAccount = "hoop-create-serviceaccount"
	EventUpdateServiceAccount = "hoop-update-serviceaccount"

	// review
	EventUpdateReview = "hoop-update-review"
	EventFetchReviews = "hoop-fetch-reviews"

	// agent
	EventCreateAgent         = "hoop-create-agent"
	EventCreateStandardAgent = "hoop-create-standard-agent"
	EventCreateEmbeddedAgent = "hoop-create-embedded-agent"
	EventDeleteAgent         = "hoop-delete-agent"

	// plugins
	EventCreatePlugin          = "hoop-create-plugin"
	EventUpdatePlugin          = "hoop-update-plugin"
	EventUpdatePluginConfig    = "hoop-update-plugin-config"
	EventOpenWebhooksDashboard = "hoop-open-webhooks-dashboard"

	//Jira
	EventCreateJiraIntegration = "hoop-create-jira-integration"
	EventUpdateJiraIntegration = "hoop-update-jira-integration"

	// features
	EventOrgFeatureUpdate            = "hoop-org-feature-update"
	EventFeatureAskAIChatCompletions = "hoop-feature-askai-chat-completions"

	// search api
	EventSearch = "hoop-search"

	// exec
	EventGrpcExec          = "hoop-grpc-exec"
	EventApiExecConnection = "hoop-api-exec-connection" // endpoint deprecated
	EventApiExecSession    = "hoop-api-exec-session"
	EventExecRunbook       = "hoop-exec-runbook"
	EventApiExecReview     = "hoop-api-exec-review"

	// connect
	EventGrpcConnect            = "hoop-grpc-connect"
	EventApiProxymanagerConnect = "hoop-api-proxymanager-connect"
)
