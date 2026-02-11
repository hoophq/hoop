package openapi

import (
	"encoding/json"
	"time"

	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
)

type HTTPError struct {
	Message string `json:"message" example:"the error description"`
}

type Login struct {
	// The URL to redirect the user to the identity provider
	URL string `json:"login_url"`
}

type SignupRequest struct {
	// Organization ID
	OrgID string `json:"org_id" format:"uuid" readonly:"true"`
	// Name of the organization
	OrgName string `json:"org_name" binding:"required,min=2,max=100"`
	// Display name of the user
	ProfileName string `json:"profile_name" binding:"max=255"`
	// Link containing the picture to display
	ProfilePicture string `json:"profile_picture" binding:"max=2048"`
}

type (
	StatusType string
	RoleType   string
)

const (
	StatusActive   StatusType = "active"
	StatusInactive StatusType = "inactive"
	StatusInvited  StatusType = "invited"

	// RoleAdminType will grant access to all routes.
	RoleAdminType RoleType = "admin"
	// RoleAuditorType grants read only access to session related routes
	RoleAuditorType RoleType = "auditor"
	// RoleStandardType will grant access to standard routes
	RoleStandardType RoleType = "standard"
	// RoleUnregisteredType will grant access to unregistered routes only
	// All authenticated and non registered users represents this role
	RoleUnregisteredType RoleType = "unregistered"
)

type User struct {
	// Unique identifier of the resource
	ID string `json:"id" format:"uuid" readonly:"true"`
	// Display name
	Name string `json:"name" example:"John Wick"`
	// Email address of the user
	Email string `json:"email" format:"email" binding:"required"`
	// The status of the user. Inactive users cannot access the system
	Status StatusType `json:"status" default:"active"`
	// DEPRECATED in flavor of role
	Verified bool `json:"verified" readonly:"true"`
	// Permission related to the user
	// * admin - Has super privileges and has access to any resource in the system
	// * standard - Grant access to standard routes.
	// * unregistered - Grant access to unregistered routes. It's a transient state where the user is authenticated but is not registered.
	// This state is only available for multi tenant environments
	Role string `json:"role" enums:"admin,standard,unregistered" readonly:"true" example:"standard"`
	// The identifier of slack to send messages to users
	SlackID string `json:"slack_id" example:"U053ELZHB53"`
	// The profile picture url to display
	Picture string `json:"picture" example:""`
	// Groups registered for this user
	Groups []string `json:"groups" example:"sre,dba"`
	// Local auth cases have a password
	Password string `json:"password,omitempty" example:"mysecurepassword"`
}

type UserPatchSlackID struct {
	SlackID string `json:"slack_id" binding:"required" example:"U053ELZHB53"`
}

type UserGroup struct {
	// Name of the user group
	Name string `json:"name" binding:"required" example:"engineering"`
}

type UserInfo struct {
	User `json:",inline"`
	// DEPRECATED in flavor of role
	IsAdmin bool `json:"is_admin"`
	// DEPRECATED is flavor of tenancy_type
	IsMultitenant bool `json:"is_multitenant"`
	// The gateway tenancy type
	// * selfhosted - Single tenant gateway, organization is registered on gateway startup and signup is performed on login
	// * multitenant - Allows multiple organization through a signup process
	TenancyType string `json:"tenancy_type" enums:"selfhosted,multitenant"`
	// Organization unique identifier
	OrgID string `json:"org_id" format:"uuid"`
	// Organization name
	OrgName string `json:"org_name" default:"JohnWickCorp"`
	// DEPRECATED in flavor of license route
	OrgLicense string `json:"org_license" example:""`
	// Ask AI feature uses ChatGPT allowing using natural language to construct input based on the context of connections
	// * unavailable - the ChatGPT credentials is not available
	// * enabled - ChatGPT credentials is available and an administrator has provide consent to send introspection schema to GTP-4
	// * disabled - ChatGPT credentials is available and an administrator has not provided consent to send introspection schema to GTP-4
	FeatureAskAI string `json:"feature_ask_ai" enums:"unavailable,enabled,disabled"`
	// Enable or disable Webapp users management
	// * on - Enable the users management view on Webapp
	// * on - Disable the users management view on Webapp
	WebAppUsersManagement  string `json:"webapp_users_management" enums:"on,off" default:"on"`
	IntercomUserHmacDigest string `json:"intercom_hmac_digest"`
}

type ServiceAccountStatusType string

const (
	ServiceAccountStatusActive   ServiceAccountStatusType = "active"
	ServiceAccountStatusInactive ServiceAccountStatusType = "inactive"
)

type ServiceAccount struct {
	// The unique identifier of this resource
	ID string `json:"id" readonly:"true" format:"uuid" example:"BF997324-5A27-4778-806A-41EE83598494"`
	// Organization ID
	OrgID string `json:"org_id" readonly:"true" format:"uuid"`
	// Subject is the external identifier that maps the user from the identity provider.
	// This field is immutable after creation
	Subject string `json:"subject" binding:"required" example:"bJ8xV3ASWGTi7L9Z6zvHKqxJlnZM5TxV1bRdc0706vW"`
	// The display name of this service account
	Name string `json:"name" example:"system-automation"`
	// Inactive service account will not be able to access the api
	Status ServiceAccountStatusType `json:"status" binding:"required" enums:"active,inactive"`
	// The groups in which this service account belongs to
	Groups []string `json:"groups" example:"engineering"`
}

type AgentRequest struct {
	// Unique name of the resource
	Name string `json:"name" binding:"required" example:"default"`
	// Mode of execution of the agent
	// * standard - Is the default mode, which is suitable to run the agent as a standalone process
	// * embedded - This mode is suitable when the agent needs to be run close to another process or application
	Mode string `json:"mode" default:"standard" enums:"standard,embedded"`
}

type AgentCreateResponse struct {
	// Token is the key in a DSN format: grpc|grpcs://<name>:<key>@<hostname>:<port>?mode=standard|embedded
	Token string `json:"token" example:"grpc://default:xagt-zKQQA9PAjCVJ4O8VlE2QZScNEbfmFisg_OerkI21NEg@127.0.0.1:8010?mode=standard"`
}

type AgentResponse struct {
	// Unique ID of the resource
	ID string `json:"id" format:"uuid" example:"8a4239fa-5116-4bbb-ad3c-ea1f294aac4a"`
	// The token/key of the resource, this value is always empty for now
	Token string `json:"token" default:""`
	// Unique name of the resource
	Name string `json:"name" example:"default"`
	// Mode of execution of the agent
	// * standard - Is the default mode, which is suitable to run the agent as a standalone process
	// * embedded - This mode is suitable when the agent needs to be run close to another process or application
	Mode string `json:"mode" enums:"standard,embedded" example:"standard"`
	// The status of the agent
	// * CONNECTED - The agent is connected with the gateway
	// * DISCONNECTED - The agent is disconnected from the gateway
	Status string `json:"status" enums:"CONNECTED,DISCONNECTED" example:"DISCONNECTED"`
	// Metadata contains attributes regarding the machine where the agent is being executed
	// * version - Version of the agent
	// * go-version - Agent build information
	// * compiler - Agent build information
	// * platform - The operating system architecture where the agent is running
	// * machine-id - The machine id of server
	// * kernel-version - The kernel version of the server
	Metadata map[string]string `json:"metadata" example:"hostname:johnwick.local,version:1.23.14,compiler:gcc,kernel-version:Linux 9acfe93d8195 5.15.49-linuxkit,platform:amd64,machine-id:id"`
	// DEPRECATE top level metadata keys
	Hostname      string `json:"hostname" example:"john.wick.local"`
	MachineID     string `json:"machine_id" example:""`
	KernelVersion string `json:"kernel_version" example:"Linux 9acfe93d8195 5.15.49-linuxkit"`
	Version       string `json:"version" example:"1.23.10"`
	GoVersion     string `json:"goversion" example:"1.22.4"`
	Compiler      string `json:"compiler" example:"gcc"`
	Platform      string `json:"platform" example:"amd64"`
}

type Connection struct {
	// Unique ID of the resource
	ID string `json:"id" readonly:"true" format:"uuid" example:"5364ec99-653b-41ba-8165-67236e894990"`
	// Name of the connection. This attribute is immutable when updating it
	Name string `json:"name" binding:"required" example:"pgdemo"`
	// Is the shell command that is going to be executed when interacting with this connection.
	// This value is required if the connection is going to be used from the Webapp.
	Command []string `json:"command" example:"/bin/bash"`
	// Resource to which this connection belongs to, it'll be created if it doesn't exist
	ResourceName string `json:"resource_name" example:"pgdemo"`
	// Type represents the main type of the connection:
	// * database - Database protocols
	// * application - Custom applications
	// * custom - Shell applications
	Type string `json:"type" binding:"required" enums:"database,application,custom" example:"database"`
	// Sub Type is the underline implementation of the connection:
	// * postgres - Implements Postgres protocol
	// * mysql - Implements MySQL protocol
	// * mongodb - Implements MongoDB Wire Protocol
	// * mssql - Implements Microsoft SQL Server Protocol
	// * oracledb - Implements Oracle Database Protocol
	// * tcp - Forwards a TCP connection
	// * ssh - Forwards a SSH connection
	// * httpproxy - Forwards a HTTP connection
	// * dynamodb - AWS DynamoDB experimental integration
	// * cloudwatch - AWS CloudWatch experimental integration
	SubType string `json:"subtype" example:"postgres"`
	// Secrets are environment variables that are going to be exposed
	// in the runtime of the connection:
	// * { envvar:[env-key]: [base64-val] } - Expose the value as environment variable
	// * { filesystem:[env-key]: [base64-val] } - Expose the value as a temporary file path creating the value in the filesystem
	//
	// The value could also represent an integration with a external provider:
	// * { envvar:[env-key]: _aws:[secret-name]:[secret-key] } - Obtain the value dynamically in the AWS secrets manager and expose as environment variable
	// * { envvar:[env-key]: _envjson:[json-env-name]:[json-env-key] } - Obtain the value dynamically from a JSON env in the agent runtime. Example: MYENV={"KEY": "val"}
	Secrets map[string]any `json:"secret"`
	// Default databases returns the configured value of the attribute secrets->'DB'
	DefaultDatabase string `json:"default_database"`
	// The agent associated with this connection
	AgentId string `json:"agent_id" binding:"required" format:"uuid" example:"1837453e-01fc-46f3-9e4c-dcf22d395393"`
	// Status is a read only field that informs if the connection is available for interaction
	// * online - The agent is connected and alive
	// * offline - The agent is not connected
	Status string `json:"status" readonly:"true" enums:"online,offline"`
	// Reviewers is a list of groups that will review the connection before the user could execute it
	Reviewers []string `json:"reviewers" example:"dba-group"`
	// When this option is enabled it will allow managing the redact types through the attribute `redact_types`
	RedactEnabled bool `json:"redact_enabled"`
	// Redact Types is a list of info types that will used to redact the output of the connection.
	// Possible values are described in the DLP documentation: https://cloud.google.com/sensitive-data-protection/docs/infotypes-reference
	RedactTypes []string `json:"redact_types" example:"EMAIL_ADDRESS"`
	// Managed By is a read only field that indicates who is managing this resource.
	// When this attribute is set, this resource is considered immutable
	ManagedBy *string `json:"managed_by" readonly:"true" example:""`
	// DEPRECATED: Tags to classify the connection
	Tags []string `json:"tags" example:"prod"`
	// Tags to identify the connection
	// * keys must contain between 1 and 64 alphanumeric characters, it may include (-), (_), (/), or (.) characters and it must not end with (-), (/) or (-).
	// * values must contain between 1 and 256 alphanumeric characters, it may include space, (-), (_), (/), (+), (@), (:), (=) or (.) characters.
	ConnectionTags map[string]string `json:"connection_tags" example:"environment:prod,tier:frontend"`
	// Toggle Ad Hoc Runbooks Executions
	// * enabled - Enable to run runbooks for this connection
	// * disabled - Disable runbooks execution for this connection
	AccessModeRunbooks string `json:"access_mode_runbooks" binding:"required" enums:"enabled,disabled"`
	// Toggle Ad Hoc Executions
	// * enabled - Enable to run ad-hoc executions for this connection
	// * disabled - Disable ad-hoc executions for this connection
	AccessModeExec string `json:"access_mode_exec" binding:"required" enums:"enabled,disabled"`
	// Toggle Port Forwarding
	// * enabled - Enable to perform port forwarding for this connection
	// * disabled - Disable port forwarding for this connection
	AccessModeConnect string `json:"access_mode_connect" binding:"required" enums:"enabled,disabled"`
	// Toggle Introspection Schema
	// * enabled - Enable the instrospection schema in the webapp
	// * disabled - Disable the instrospection schema in the webapp
	AccessSchema string `json:"access_schema" binding:"required" enums:"enabled,disabled"`
	// The guard rail association id rules
	GuardRailRules []string `json:"guardrail_rules" example:"5701046A-7B7A-4A78-ABB0-A24C95E6FE54,B19BBA55-8646-4D94-A40A-C3AFE2F4BAFD"`
	// The jira issue templates ids associated to the connection
	JiraIssueTemplateID string `json:"jira_issue_template_id" example:"B19BBA55-8646-4D94-A40A-C3AFE2F4BAFD"`
	// Groups that can force approve reviews for this connection
	ForceApproveGroups []string `json:"force_approve_groups" example:"sre-team"`
	// Maximum duration in seconds for JIT access sessions on this connection
	AccessMaxDuration *int `json:"access_max_duration" example:"3600"`
	// Minimum number of review approvals required to execute this connection
	MinReviewApprovals *int `json:"min_review_approvals" example:"2"`
}

type ConnectionPatch struct {
	// Is the shell command that is going to be executed when interacting with this connection.
	// This value is required if the connection is going to be used from the Webapp.
	Command *[]string `json:"command" example:"/bin/bash"`
	// Type represents the main type of the connection:
	// * database - Database protocols
	// * application - Custom applications
	// * custom - Shell applications
	Type *string `json:"type" enums:"database,application,custom" example:"database"`
	// Sub Type is the underline implementation of the connection:
	// * postgres - Implements Postgres protocol
	// * mysql - Implements MySQL protocol
	// * mongodb - Implements MongoDB Wire Protocol
	// * mssql - Implements Microsoft SQL Server Protocol
	// * oracledb - Implements Oracle Database Protocol
	// * tcp - Forwards a TCP connection
	// * ssh - Forwards a SSH connection
	// * httpproxy - Forwards a HTTP connection
	// * dynamodb - AWS DynamoDB experimental integration
	// * cloudwatch - AWS CloudWatch experimental integration
	SubType *string `json:"subtype" example:"postgres"`
	// Secrets are environment variables that are going to be exposed
	// in the runtime of the connection:
	// * { envvar:[env-key]: [base64-val] } - Expose the value as environment variable
	// * { filesystem:[env-key]: [base64-val] } - Expose the value as a temporary file path creating the value in the filesystem
	//
	// The value could also represent an integration with a external provider:
	// * { envvar:[env-key]: _aws:[secret-name]:[secret-key] } - Obtain the value dynamically in the AWS secrets manager and expose as environment variable
	// * { envvar:[env-key]: _envjson:[json-env-name]:[json-env-key] } - Obtain the value dynamically from a JSON env in the agent runtime. Example: MYENV={"KEY": "val"}
	Secrets *map[string]any `json:"secret"`
	// The agent associated with this connection
	AgentId *string `json:"agent_id" format:"uuid" example:"1837453e-01fc-46f3-9e4c-dcf22d395393"`
	// Reviewers is a list of groups that will review the connection before the user could execute it
	Reviewers *[]string `json:"reviewers" example:"dba-group"`
	// Redact Types is a list of info types that will used to redact the output of the connection.
	// Possible values are described in the DLP documentation: https://cloud.google.com/sensitive-data-protection/docs/infotypes-reference
	RedactTypes *[]string `json:"redact_types" example:"EMAIL_ADDRESS"`
	// DEPRECATED: Tags to classify the connection
	Tags *[]string `json:"tags" example:"prod"`
	// Tags to identify the connection
	// * keys must contain between 1 and 64 alphanumeric characters, it may include (-), (_), (/), or (.) characters and it must not end with (-), (/) or (-).
	// * values must contain between 1 and 256 alphanumeric characters, it may include space, (-), (_), (/), (+), (@), (:), (=) or (.) characters.
	ConnectionTags *map[string]string `json:"connection_tags" example:"environment:prod,tier:frontend"`
	// Toggle Ad Hoc Runbooks Executions
	// * enabled - Enable to run runbooks for this connection
	// * disabled - Disable runbooks execution for this connection
	AccessModeRunbooks *string `json:"access_mode_runbooks" enums:"enabled,disabled"`
	// Toggle Ad Hoc Executions
	// * enabled - Enable to run ad-hoc executions for this connection
	// * disabled - Disable ad-hoc executions for this connection
	AccessModeExec *string `json:"access_mode_exec" enums:"enabled,disabled"`
	// Toggle Port Forwarding
	// * enabled - Enable to perform port forwarding for this connection
	// * disabled - Disable port forwarding for this connection
	AccessModeConnect *string `json:"access_mode_connect" enums:"enabled,disabled"`
	// Toggle Introspection Schema
	// * enabled - Enable the instrospection schema in the webapp
	// * disabled - Disable the instrospection schema in the webapp
	AccessSchema *string `json:"access_schema" enums:"enabled,disabled"`
	// The guard rail association id rules
	GuardRailRules *[]string `json:"guardrail_rules" example:"5701046A-7B7A-4A78-ABB0-A24C95E6FE54,B19BBA55-8646-4D94-A40A-C3AFE2F4BAFD"`
	// The jira issue templates ids associated to the connection
	JiraIssueTemplateID *string `json:"jira_issue_template_id" example:"B19BBA55-8646-4D94-A40A-C3AFE2F4BAFD"`
}

type ConnectionTagCreateRequest struct {
	// Key is the identifier for the tag category (e.g., "environment", "department")
	Key string `json:"key" binding:"required" example:"environment"`
	// Value is the specific tag value associated with the key (e.g., "production", "finance")
	Value string `json:"value" binding:"required" example:"production"`
}

type ConnectionTagUpdateRequest struct {
	// Value is the new tag value to be assigned to the existing key
	Value string `json:"value" binding:"required" example:"staging"`
}

type ConnectionTagList struct {
	Items []ConnectionTag `json:"items"`
}

type ConnectionTag struct {
	// ID is the unique identifier for this specific tag
	ID string `json:"id" example:"tag_01H7ZD5SJRZ7RPGQRMT4Y9HF"`
	// Key is the identifier for the tag category (e.g., "environment", "department")
	Key string `json:"key" example:"environment"`
	// Value is the specific tag value associated with the key (e.g., "production", "finance")
	Value string `json:"value" example:"production"`
	// UpdatedAt is the timestamp when this tag was last updated
	UpdatedAt time.Time `json:"updated_at" example:"2023-08-15T14:30:45Z"`
	// CreatedAt is the timestamp when this tag was created
	CreatedAt time.Time `json:"created_at" example:"2023-08-15T14:30:45Z"`
}

type ExecRequest struct {
	// The input of the execution
	Script string `json:"script" example:"echo 'hello from hoop'"`
	// The target connection
	Connection string `json:"connection" example:"bash"`
	// DEPRECATED in flavor of metadata
	Labels map[string]string `json:"labels"`
	// Metadata contains attributes that is going to be available in the Session resource
	Metadata map[string]any `json:"metadata"`
	// Additional arguments that will be joined when construction the command to be executed
	ClientArgs []string `json:"client_args" example:"--verbose"`
}

type ExecResponse struct {
	// Inform if the connection has review enabled
	HasReview bool `json:"has_review" example:"false"`
	// Each execution creates a unique session id
	SessionID string `json:"session_id" format:"uuid" example:"5701046A-7B7A-4A78-ABB0-A24C95E6FE54"`
	// Output contains an utf-8 output containing the outcome of the ad-hoc execution
	Output string `json:"output"`
	// Status reports if the outcome of the execution
	// * success - The execution was executed with success
	// * failed - In case of internal error or when the agent returns an exit code greater than 0 or different than -2
	// * running - The execution may still be running.
	OutputStatus string `json:"output_status" enums:"success,failed,running" example:"failed"`
	// If the `output`` field is truncated or not
	Truncated bool `json:"truncated" example:"false"`
	// The amount of time the execution took in miliseconds
	ExecutionTimeMili int64 `json:"execution_time" example:"5903"`
	// The shell exit code, any non zero code means an error
	// * 0 - Linux success exit code
	// * -2 - internal gateway code that means it was unable to obtain a valid exit code number from the agent outcome packet
	// * 254 - internal agent code that means it was unable to obtain a valid exit code number from the process
	ExitCode int `json:"exit_code" example:"1"`
}

type RunbookRequest struct {
	// The relative path name of the runbook file from the git source
	FileName string `json:"file_name" binding:"required" example:"myrunbooks/run-backup.runbook.sql"`
	// The commit sha reference to obtain the file
	RefHash string `json:"ref_hash" example:"20320ebbf9fc612256b67dc9e899bbd6e4745c77"`
	// The parameters of the runbook. It must match with the declared attributes
	Parameters map[string]string `json:"parameters" example:"amount:10,wallet_id:6736"`
	// Environment Variables that will be included in the runtime
	// * { envvar:[env-key]: [base64-val] } - Expose the value as environment variable
	// * { filesystem:[env-key]: [base64-val] } - Expose the value as a temporary file path creating the value in the filesystem
	EnvVars map[string]string `json:"env_vars" example:"envvar:PASSWORD:MTIz,filesystem:SECRET_FILE:bXlzZWNyZXQ="`
	// Additional arguments to pass down to the connection
	ClientArgs []string `json:"client_args" example:"--verbose"`
	// Metadata attributes to add in the session
	Metadata map[string]any `json:"metadata"`
	// Jira fields to create a Jira issue
	JiraFields map[string]string `json:"jira_fields"`
	// Batch identifier to group sessions that were executed simultaneously
	SessionBatchID *string `json:"session_batch_id,omitempty" example:"batch-abc-123"`
}

type RunbookList struct {
	Items []*Runbook `json:"items"`
	// The commit sha
	Commit string `json:"commit" example:"03c25fd64c74712c71798250d256d4b859dd5853"`
	// The commit author
	CommitAuthor string `json:"commit_author" example:"John Wick <john.wick@bad.org>"`
	// The commit message
	CommitMessage string `json:"commit_message" example:"runbook update"`
}

type Runbook struct {
	// File path relative to repository root containing runbook file in the following format: `/path/to/file.runbook.<ext>`
	Name string `json:"name" example:"ops/update-user.runbook.sh"`
	// Metadata contains the attributes parsed from a template.
	// Payload Example:
	/*
		{
			"customer_id" : {
				"description": "the id of the customer",
				"required": true,
				"type": "text",
				"default": "Default value to use",
				"order": 1
			},
			"country": {
				"description": "the country code US; BR, etc",
				"required": false,
				"type": "select",
				"options": ["US", "BR"],
				"order": 2
			}
		}
	*/
	// By default it will have the attributes `description=""`, `required=false` and `type="text"`.
	// Optional attributes include `order` (int), `default`, `placeholder`, `options`, and `asenv`.
	Metadata map[string]any `json:"metadata"`
	// The connections that could be used for this runbook
	ConnectionList []string `json:"connections,omitempty" example:"pgdemo,bash"`
	// The error description if it failed to render
	Error      *string           `json:"error"`
	EnvVars    map[string]string `json:"-"`
	InputFile  []byte            `json:"-"`
	CommitHash string            `json:"-"`
}

type SessionList struct {
	Items       []Session `json:"data"`
	Total       int64     `json:"total" example:"100"`
	HasNextPage bool      `json:"has_next_page"`
}

type (
	SessionEventStream                   []any
	SessionScriptType                    map[string]string
	SessionLabelsType                    map[string]string
	SessionNonIndexedEventStreamListType map[string][]SessionEventStream
	SessionOptionKey                     string
)

type SessionEventStreamType string

const (
	SessionEventStreamUTF8Type       SessionEventStreamType = "utf8"
	SessionEventStreamBase64Type     SessionEventStreamType = "base64"
	SessionEventStreamRawQueriesType SessionEventStreamType = "raw-queries"
)

type SessionGetByIDParams struct {
	// The file extension to donwload the session as a file content.
	// * `csv` - it will parse the content to format in csv format
	// * `json` - it will parse the content as a json stream.
	// * `<any-format>` - No special parsing is applied
	Extension string `json:"extension" example:"csv"`
	// Choose the type of events to include
	// * `i` - Input (stdin)
	// * `o` - Output (stdout)
	// * `e` - Error (stderr)
	Events []string `json:"events" example:"i,o,e"`
	// Construct the file content adding a break line when parsing each event
	NewLine string `json:"new_line" enums:"0,1" example:"1" default:"0"`
	// Construct the file content adding the event time as prefix when parsing each event
	EventTime string `json:"event-time" enums:"0,1" example:"1" default:"0"`
	// Parse available options for the event stream
	// * `utf8` - parse the session output (o) and error (e) events as utf-8 content in the session payload
	// * `base64` - parse the session output (o) and error (e) events as base64 content in the session payload
	// * `raw-queries` - encode each event stream parsing the input of queries based on the database wire protocol (available databases: postgres)
	EventStream SessionEventStreamType `json:"event_stream" default:""`
	// Expand the given attributes
	Expand string `json:"expand" enums:"event_stream,session_input" example:"event_stream" default:""`
}

type SessionOption struct {
	OptionKey SessionOptionKey
	OptionVal any
}

const (
	SessionOptionUser                SessionOptionKey = "user"
	SessionOptionType                SessionOptionKey = "type"
	SessionOptionConnection          SessionOptionKey = "connection"
	SessionOptionReviewStatus        SessionOptionKey = "review.status"
	SessionOptionReviewApproverEmail SessionOptionKey = "review.approver"
	SessionOptionBatchID             SessionOptionKey = "batch_id"
	SessionOptionStartDate           SessionOptionKey = "start_date"
	SessionOptionEndDate             SessionOptionKey = "end_date"
	SessionOptionOffset              SessionOptionKey = "offset"
	SessionOptionLimit               SessionOptionKey = "limit"
	SessionOptionJiraIssueKey        SessionOptionKey = "jira_issue_key"
)

var AvailableSessionOptions = []SessionOptionKey{
	SessionOptionUser,
	SessionOptionType,
	SessionOptionConnection,
	SessionOptionReviewStatus,
	SessionOptionReviewApproverEmail,
	SessionOptionBatchID,
	SessionOptionStartDate,
	SessionOptionEndDate,
	SessionOptionLimit,
	SessionOptionOffset,
	SessionOptionJiraIssueKey,
}

type SessionStatusType string

const (
	SessionStatusOpen  SessionStatusType = "open"
	SessionStatusReady SessionStatusType = "ready"
	SessionStatusDone  SessionStatusType = "done"
)

type Session struct {
	// The resource unique identifier
	ID string `json:"id" format:"uuid" example:"1CBC8DB5-FBF8-4293-8E35-59A6EEA40207"`
	// The organization unique identifier
	OrgID string `json:"org_id" format:"uuid" example:"0CD7F941-2BB8-4F9F-93B0-11620D4652AB"`
	// The input of the session. This value is only set for the verb `exec`
	Script SessionScriptType `json:"script" example:"data:SELECT NOW()"`
	// The input size of the session in bytes
	ScriptSize int64 `json:"script_size" example:"12"`
	// DEPRECATED in flavor of metrics and metadata
	Labels SessionLabelsType `json:"labels"`
	// Metadata attributes related to integrations with third party services
	IntegrationsMetadata map[string]any `json:"integrations_metadata"`
	Metadata             map[string]any `json:"metadata"`
	// Refactor to use a struct
	Metrics map[string]any `json:"metrics"`
	// The user email of the resource
	UserEmail string `json:"user"`
	// The user subject identifier of the resource
	UserID string `json:"user_id" example:"nJ1xV3ASWGTi7L8Y6zvnKqxNlnZM2TxV1bRdc0706vZ"`
	// The user display name of this resource
	UserName string `json:"user_name" example:"John Wick"`
	// The connection type of this resource
	Type string `json:"type" example:"database"`
	// The subtype of the connection
	ConnectionSubtype string `json:"connection_subtype" example:"postgres"`
	// The connection name of this resource
	Connection string `json:"connection" example:"pgdemo"`
	// The tags of the connection resource
	ConnectionTags map[string]string `json:"connection_tags" example:"team:banking;environment:prod"`
	// Review of this session. In case the review doesn't exist this field will be null
	Review *SessionReview `json:"review"`
	// Verb is how the client has interacted with this resource
	// * exec - Is an ad-hoc shell execution
	// * connect - Interactive execution, protocol port forwarding or interactive shell session
	Verb string `json:"verb" enums:"connect,exec"`
	// Status of the resource
	// * ready - the resource is ready to be executed, after being approved by a user
	// * open - the session started and it's running
	// * done - the session has finished
	Status SessionStatusType `json:"status"`
	// The Linux exit code if it's available
	ExitCode *int `json:"exit_code"`
	// The stream containing the output of the execution in the following format
	//
	// `[[0.268589438, "i", "ZW52"], ...]`
	//
	// * `<event-time>` - relative time in miliseconds to start_date
	// * `<event-type>` - the event type as string (i: input, o: output e: output-error)
	// * `<base64-content>` - the content of the session encoded as base64 string
	EventStream json.RawMessage `json:"event_stream,omitempty" swagger:"type:string"`
	// The stored resource size in bytes.
	// When any parsing is applied to the request the value display the computed parsed size.
	// The pre-computed size will be available in the attribute `metrics.event_size`
	EventSize int64 `json:"event_size" example:"569"`
	// Batch identifier to group sessions that were executed simultaneously
	SessionBatchID *string `json:"session_batch_id,omitempty" example:"batch-abc-123"`
	// When the execution started
	StartSession time.Time `json:"start_date" example:"2024-07-25T15:56:35.317601Z"`
	// When the execution ended. A null value indicates the session is still running
	EndSession *time.Time `json:"end_date" example:"2024-07-25T15:56:35.361101Z"`
}

type ProvisionSession struct {
	UserEmail         string                   `json:"user_email" binding:"required" example:"johnwick@bad.org"`
	Script            string                   `json:"script"`
	Connection        string                   `json:"connection" binding:"required" example:"pgdemo"`
	ApprovedReviewers []string                 `json:"approved_reviewers"`
	EnvVars           map[string]string        `json:"env_vars"`
	AccessDurationSec *int64                   `json:"access_duration_sec"`
	Metadata          map[string]any           `json:"metadata"`
	ClientArgs        []string                 `json:"client_args"`
	JiraFields        map[string]string        `json:"jira_fields"`
	TimeWindow        *ReviewSessionTimeWindow `json:"time_window"`
}

type ProvisionSessionResponse struct {
	SessionID string `json:"session_id" format:"uuid" example:"5701046A-7B7A-4A78-ABB0-A24C95E6FE54"`
	UserEmail string `json:"user_email" example:"johnwick@bad.org"`
	HasReview bool   `json:"has_review" example:"false"`
}

type SessionUpdateMetadataRequest struct {
	// The metadata field
	Metadata map[string]any `json:"metadata" swaggertype:"object,string" example:"reason:fix-issue"`
}

type SessionReportParams struct {
	// Group by this field
	GroupBy string `json:"group_by" enums:"connection,connection_type,id,user_email" default:"connection" example:"connection_type"`
	// Filter the report by the ID of the session
	ID string `json:"id" default:"" example:"FF970D17-23C6-4254-ABE6-103A9DDF30EE"`
	// Filter the report by the type of the verb
	Verb string `json:"verb" enums:"exec,connect" default:"" example:"exec"`
	// Filter by the connection name
	ConnectionName string `json:"connection_name" default:"" example:"pgdemo"`
	// Filter the report by e-mail
	UserEmail string `json:"user_email" default:"" example:"johnwick@bad.org"`
	// Start Date, default to current date
	StartDate string `json:"start_date" format:"date" example:"2024-07-29"`
	// End Date, default to current date + 1 day
	EndDate string `json:"end_date" format:"date" example:"2024-07-30"`
}

type SessionReport struct {
	Items []SessionReportItem `json:"items"`
	// The sum of `items[].redact_total`
	TotalRedactCount int64 `json:"total_redact_count" example:"12"`
	// The sum of `items[].transformed_bytes`
	TotalTransformedBytes int64 `json:"total_transformed_bytes" example:"40293"`
}

type SessionReportItem struct {
	// The value of the group_by resource (connection, session id, email or connection type)
	ResourceName string `json:"resource" example:"connection"`
	// The info type name
	InfoType string `json:"info_type" example:"EMAIL_ADDRESS"`
	// The total redacts in which this info type was found for this resource
	RedactTotal int64 `json:"redact_total" example:"23"`
	// The total transformed bytes performed by this info type
	TransformedBytes int64 `json:"transformed_bytes" example:"30012"`
}

type (
	ReviewStatusType        string
	ReviewRequestStatusType string
	ReviewType              string
	ReviewTimeWindowType    string
)

const (
	ReviewStatusPending    ReviewStatusType = "PENDING"
	ReviewStatusApproved   ReviewStatusType = "APPROVED"
	ReviewStatusRejected   ReviewStatusType = "REJECTED"
	ReviewStatusRevoked    ReviewStatusType = "REVOKED"
	ReviewStatusProcessing ReviewStatusType = "PROCESSING"
	ReviewStatusExecuted   ReviewStatusType = "EXECUTED"
	ReviewStatusUnknown    ReviewStatusType = "UNKNOWN"

	ReviewStatusRequestApprovedType ReviewRequestStatusType = ReviewRequestStatusType(ReviewStatusApproved)
	ReviewStatusRequestRejectedType ReviewRequestStatusType = ReviewRequestStatusType(ReviewStatusRejected)
	ReviewStatusRequestRevokedType  ReviewRequestStatusType = ReviewRequestStatusType(ReviewStatusRevoked)

	ReviewTypeJit     ReviewType = "jit"
	ReviewTypeOneTime ReviewType = "onetime"

	ReviewTimeWindowTypeTimeRange ReviewTimeWindowType = "time_range"
)

type ReviewRequest struct {
	// The reviewed status
	// * APPROVED - Approve the review resource
	// * REJECTED - Reject the review resource
	// * REVOKED - Revoke an approved review
	Status      ReviewRequestStatusType  `json:"status" binding:"required" example:"APPROVED"`
	TimeWindow  *ReviewSessionTimeWindow `json:"time_window"`
	ForceReview bool                     `json:"force_review" example:"false"`
}

type SessionReview struct {
	// Resource identifier
	ID string `json:"id" format:"uuid" readonly:"true" example:"9F9745B4-C77B-4D52-84D3-E24F67E3623C"`
	// The type of the review
	// * onetime - Represents a one time execution
	// * jit - Represents a time based review
	Type ReviewType `json:"type" enums:"onetime,jit" readonly:"true"`
	// The amount of time (nanoseconds) to allow access to the connection. It's valid only for `jit` type reviews
	AccessDuration time.Duration `json:"access_duration" swaggertype:"integer" readonly:"true" default:"1800000000000" example:"0"`
	// The status of the review
	// * PENDING - The resource is waiting to be reviewed
	// * APPROVED - The resource is fully approved
	// * REJECTED - The resource is fully rejected
	// * REVOKED - The resource was revoked after being approved
	// * PROCESSING - The review is being executed
	// * EXECUTED - The review was executed
	// * UNKNOWN - Unable to know the status of the review
	Status ReviewStatusType `json:"status"`
	// The time when this review was revoked
	RevokeAt *time.Time `json:"revoke_at" readonly:"true" example:""`
	// The time the resource was created
	CreatedAt time.Time `json:"created_at" readonly:"true" example:"2024-07-25T15:56:35.317601Z"`
	// Contains the groups that requires to approve this review
	ReviewGroupsData []ReviewGroup `json:"review_groups_data" readonly:"true"`
	// The time window configuration that can execute the session
	TimeWindow *ReviewSessionTimeWindow `json:"time_window" readonly:"true"`
}

type ReviewSessionTimeWindow struct {
	Type          ReviewTimeWindowType `json:"type" binding:"required" enums:"time_range"`
	Configuration map[string]string    `json:"configuration" binding:"required" example:"start_time:09:00,end_time:18:00"`
}

type Review struct {
	// Resource identifier
	ID string `json:"id" format:"uuid" readonly:"true" example:"9F9745B4-C77B-4D52-84D3-E24F67E3623C"`
	// The id of session
	Session string `json:"session" format:"uuid" readonly:"true" example:"35DB0A2F-E5CE-4AD8-A308-55C3108956E5"`
	// The type of the review
	// * onetime - Represents a one time execution
	// * jit - Represents a time based review
	Type ReviewType `json:"type" enums:"onetime,jit" readonly:"true"`
	// The amount of time (nanoseconds) to allow access to the connection. It's valid only for `jit` type reviews
	AccessDuration time.Duration `json:"access_duration" swaggertype:"integer" readonly:"true" default:"1800000000000" example:"0"`
	// The status of the review
	// * PENDING - The resource is waiting to be reviewed
	// * APPROVED - The resource is fully approved
	// * REJECTED - The resource is fully rejected
	// * REVOKED - The resource was revoked after being approved
	// * PROCESSING - The review is being executed
	// * EXECUTED - The review was executed
	// * UNKNOWN - Unable to know the status of the review
	Status ReviewStatusType `json:"status"`
	// The time when this review was revoked
	RevokeAt *time.Time `json:"revoke_at" readonly:"true" example:""`
	// The time the resource was created
	CreatedAt time.Time `json:"created_at" readonly:"true" example:"2024-07-25T15:56:35.317601Z"`
	// Contains the groups that requires to approve this review
	ReviewGroupsData []ReviewGroup `json:"review_groups_data" readonly:"true"`
	// The time window configuration that can execute the session
	TimeWindow *ReviewSessionTimeWindow `json:"time_window" readonly:"true"`
}

type ReviewOwner struct {
	// The resource identifier
	ID string `json:"id,omitempty" format:"uuid" readonly:"true" example:"D5BFA2DD-7A09-40AE-AFEB-C95787BA9E90"`
	// The display name of the owner
	Name string `json:"name,omitempty" readonly:"true" example:"John Wick"`
	// The email of the owner
	Email string `json:"email" readonly:"true" example:"john.wick@bad.org"`
	// The Slack ID of the owner
	SlackID string `json:"slack_id" readonly:"true" example:"U053ELZHB53"`
}

type ReviewConnection struct {
	// The resource identifier
	ID string `json:"id,omitempty" readonly:"true" example:"20A5AABE-C35D-4F04-A5A7-C856EE6C7703"`
	// The name of the connection
	Name string `json:"name" readonly:"true" example:"pgdemo"`
}

type ReviewGroup struct {
	// The resource identifier
	ID string `json:"id" format:"uuid" readonly:"true" example:"20A5AABE-C35D-4F04-A5A7-C856EE6C7703"`
	// The group to approve this review
	Group string `json:"group" readonly:"true" example:"sre"`
	// The reviewed status
	// * APPROVED - Approve the review resource
	// * REJECTED - Reject the review resource
	// * REVOKED - Revoke an approved review
	Status ReviewRequestStatusType `json:"status" example:"APPROVED"`
	// The review owner
	ReviewedBy *ReviewOwner `json:"reviewed_by" readonly:"true"`
	// The date which this review was performed
	ReviewDate *time.Time `json:"review_date" readonly:"true" example:"2024-07-25T19:36:41Z"`
	// Indicates if this group is forcing the review
	ForcedReview bool `json:"forced_review" readonly:"true" example:"false"`
}

type Plugin struct {
	// The resource identifier
	ID string `json:"id" format:"uuid" readonly:"true" example:"15B5A2FD-0706-4A47-B1CF-B93CCFC5B3D7"`
	// The name of the plugin to enable
	// * audit - Audit connections
	// * access_control - Enable access control by groups
	// * dlp - Enable Google Data Loss Prevention (requires further configuration)
	// * indexer - Enable indexing session contents
	// * review - Enable reviewing executions
	// * runbooks - Enable configuring runbooks
	// * slack - Enable reviewing execution through Slack
	// * webhooks - Send events via webhooks
	Name string `json:"name" binding:"required" enums:"audit,access_control,dlp,indexer,review,runbooks,slack,webhooks" example:"slack"`
	// The list of connections configured for a specific plugin
	Connections []*PluginResourceConnection `json:"connections" binding:"required"`
	// The top level plugin configuration. This value is immutable after creation
	Config *PluginConfig `json:"config"`
	// DEPRECATED, should be always null
	Source *string `json:"source" default:"null"`
	// DEPRECATED, should be always 0
	Priority int `json:"priority" default:"0"`
}

type PluginConfig struct {
	// The resource identifier
	ID string `json:"id" format:"uuid" readonly:"true" example:"D9A998B3-AA7B-49B0-8463-C9E36435FC0B"`
	// The top level plugin configuration. The value is show as `REDACTED` after creation
	EnvVars map[string]string `json:"envvars" example:"SLACK_BOT_TOKEN:eG94Yi10b2tlbg==,SLACK_APP_TOKEN:eC1hcHAtdG9rZW4="`
}

type PluginResourceConnection struct {
	// The connection ID reference
	ConnectionID string `json:"id" format:"uuid" example:"B702C63C-E6EB-46BB-9D1E-90EA077E4582"`
	// The name of the connection
	Name string `json:"name" example:"pgdemo"`
	// The configuration of the plugin connection, the content depends on the plugin type
	Config []string `json:"config" example:"EMAIL_ADDRESS,URL"`
}

type PluginConnectionRequest struct {
	// The configuration of the plugin connection, the content depends on the plugin type
	Config []string `json:"config" example:"sre,devops"`
}

type PluginConnection struct {
	// The resource ID
	ID string `json:"id" format:"uuid" example:"B39D74FA-EF2E-4B0C-A28A-D382B18043C6"`
	// The plugin id to create associations
	PluginID string `json:"plugin_id" example:"review"`
	// The connection ID reference
	ConnectionID string `json:"connection_id" format:"uuid" example:"B702C63C-E6EB-46BB-9D1E-90EA077E4582"`
	// The configuration of the plugin connection, the content depends on the plugin type
	Config []string `json:"config" example:"EMAIL_ADDRESS,URL"`
	// The time when this resource was updated
	UpdatedAt time.Time `json:"updated_at" readonly:"true" example:"2024-07-25T19:36:41Z"`
}

type ProxyManagerRequest struct {
	// The connection target
	ConnectionName string `json:"connection_name" binding:"required" example:"pgdemo"`
	// The port to listen in the client
	Port string `json:"port" binding:"required" example:"5432"`
	// The access duration (in nanoseconds) of a session in case the connect has a review.
	// Default to 30 minutes
	AccessDuration time.Duration `json:"access_duration" swaggertype:"integer" example:"1800000000000"`
}

type ClientStatusType string

const (
	// ClientStatusReady indicates the grpc client is ready to
	// subscribe to a new connection
	ClientStatusReady ClientStatusType = "ready"
	// ClientStatusConnected indicates the client has opened a new session
	ClientStatusConnected ClientStatusType = "connected"
	// ClientStatusDisconnected indicates the grpc client has disconnected
	ClientStatusDisconnected ClientStatusType = "disconnected"
)

type ProxyManagerResponse struct {
	// Deterministic uuid identifier of the user
	ID string `json:"id" format:"uuid" example:"20A5AABE-C35D-4F04-A5A7-C856EE6C7703"`
	// The status of the connection request
	// * ready - indicates the grpc client is ready to subscribe to a new connection
	// * connected - indicates the client has opened a new session
	// * disconnected - indicates the grpc client has disconnected
	Status ClientStatusType `json:"status"`
	// The requested connection name
	RequestConnectionName string `json:"connection_name"`
	// The requested connection type
	RequestConnectionType string `json:"connection_type" readonly:"true"`
	// The requested connection subtype
	RequestConnectionSubType string `json:"connection_subtype" readonly:"true"`
	// Report if the connection has a review
	HasReview bool `json:"has_review" readonly:"true"`
	// The requested client port to listen
	RequestPort string `json:"port"`
	// The request access duration in case of review
	RequestAccessDuration time.Duration `json:"access_duration" swaggertype:"integer" example:"1800000000000"`
	// Metadata information about the client
	ClientMetadata map[string]string `json:"metadata" example:"session:15B3C616-6B43-4F85-B4FD-B83378A866C2,version:1.23.4,go-version:1.22.4,platform:amd64,hostname:johnwick.local"`
	// The time (RFC3339) when the client connect
	ConnectedAt string `json:"connected-at" example:"2024-07-25T19:36:41Z"`
}

type OrgKeyResponse struct {
	// The agent unique identifier
	ID string `json:"id" format:"uuid" example:"C02402E0-0175-445F-B46D-513D1F708991"`
	// The key in DSN format
	Key string `json:"key" format:"dsn" example:"grpcs://default:<secret-key>@127.0.0.1:8010"`
}

type LicensePayload struct {
	// Type of the license
	// * oss - Open Source License
	// * enterprise - Enterprise License
	Type string `json:"type" enums:"oss,enterprise" example:"enterprise"`
	// The time in timestamp the license was issued
	IssuedAt int64 `json:"issued_at" format:"timestamp" example:"1721997969"`
	// The time in timestamp the license expires
	ExpireAt int64 `json:"expire_at" format:"timestamp" example:"1722997969"`
	// The domains that are allowed to use this license.
	// * `johhwick.org` - Issue a license that is valid only for the domain `johnwick.org`
	// * `*.johhwick.org` - Issue a license that is valid for all subdomains, example: `<anydomain>.johnwick.org`
	// * `*.johhwick.org`, `*.johnwick2.org` - Issue a license that is valid for multiple wildcard subdomains
	// * `*` - Issue a license that is valid for all domains (not recommeded)
	AllowedHosts []string `json:"allowed_hosts" example:"johnwick.org,homolog.jhonwick.org,*.system.johnwick.org"`
	// The description containing information about the license
	Description string `json:"description" example:"John Wick's Bad Organization"`
}

type License struct {
	// The payload information of the license
	Payload LicensePayload `json:"payload"`
	// A sha256 identifier of the public key
	KeyID string `json:"key_id" example:"743420a8aee6f063d50e6203b588c06381020c84fea3543ff10f8470873779bc"`
	// The payload signature
	Signature string `json:"signature" example:"pA4POB1vB1yBfE+HcPD4FSPT8yY="`
}

type FeatureStatusType string

const (
	FeatureStatusEnabled  FeatureStatusType = "enabled"
	FeatureStatusDisabled FeatureStatusType = "disabled"
)

type AnalyticsTrackingStatusType string

const (
	AnalyticsTrackingEnabled  AnalyticsTrackingStatusType = "enabled"
	AnalyticsTrackingDisabled AnalyticsTrackingStatusType = "disabled"
)

var FeatureList = []string{"ask-ai"}

type FeatureRequest struct {
	// The name of the feature
	// * ask-ai - Enable and consent to use ask ai feature
	Name string `json:"name" enums:"ask-ai" binding:"required"`
	// Status of the resource
	// * enabled - The feature is consent and enable for use
	// * disabled - The feature is disabled
	Status FeatureStatusType `json:"status" binding:"required"`
}

type WebhooksDashboardResponse struct {
	URL string `json:"url" example:"https://app.svix.com/app_3ZT4NrDlps0Pjp6Af8L6pJMMh3/endpoints"`
}

type ServerLicenseInfo struct {
	// Public Key identifier of who signed the license
	KeyID string `json:"key_id" example:"f2fb0c3143822b08be26f8fc5b703e0a6689e675"`
	// The allowed hosts to use this license
	AllowedHosts []string `json:"allowed_hosts" example:"johnwick.org,homolog.johnwick.org"`
	// The type of license
	Type string `json:"type" enums:"oss,enterprise" example:"enterprise"`
	// The timestamp value when this license was issued
	IssuedAt int64 `json:"issued_at" example:"1722261321"`
	// The timestamp value when this license is valid
	ExpireAt int64 `json:"expire_at" example:"1722261422"`
	// Report if the license is valid
	IsValid bool `json:"is_valid"`
	// The error returned when verifying the license
	VerifyError string `json:"verify_error" example:"unable to verify license"`
	// The verified host (API_URL env)
	VerifiedHost string `json:"verified_host" example:"homolog.johnwick.org"`
}

type PublicServerInfo struct {
	// Auth method used by the server
	AuthMethod string `json:"auth_method" enums:"local,oidc,saml" example:"local"`
}

type IdpProviderNameType string

const (
	IdpProviderMicrosoftEntraID IdpProviderNameType = "microsoft-entra-id"
	IdpProviderOkta             IdpProviderNameType = "okta"
	IdpProviderGoogle           IdpProviderNameType = "google"
	IdpProviderAwsCognito       IdpProviderNameType = "aws-cognito"
	IdpProviderJumpCloud        IdpProviderNameType = "jumpcloud"
	IdpProviderUnknown          IdpProviderNameType = "unknown"
)

type ServerInfo struct {
	// Version of the server
	Version string `json:"version" example:"1.35.0"`
	// Commit SHA of the version
	Commit string `json:"commit_sha" example:"e6b94e86352e934b66d9c7ab2821a267dc18dfee"`
	// Log level of the server
	LogLevel string `json:"log_level" enums:"INFO,WARN,DEBUG,ERROR" example:"INFO"`
	// Expose `GODEBUG` flags enabled
	GoDebug string `json:"go_debug" example:"http2debug=2"`
	// Auth method used by the server
	AuthMethod string `json:"auth_method" enums:"oidc,local" example:"local"`
	// Redact Provider used by the server
	RedactProvider string `json:"redact_provider" enums:"gcp,mspresidio" example:"gcp"`
	// Report if GOOGLE_APPLICATION_CREDENTIALS_JSON or MSPRESIDIO is set
	HasRedactCredentials bool `json:"has_redact_credentials"`
	// Report if WEBHOOK_APPKEY is set
	HasWebhookAppKey bool `json:"has_webhook_app_key"`
	// Report if IDP_AUDIENCE env is set
	HasIDPAudience bool `json:"has_idp_audience"`
	// Report if IDP_CUSTOM_SCOPES env is set
	HasIDPCustomScopes bool `json:"has_idp_custom_scopes"`
	// Report if ASK_AI_CREDENTIALS is set (openapi credentials)
	HasAskiAICredentials bool `json:"has_ask_ai_credentials"`
	// Report if SSH_CLIENT_HOST_KEY is set
	HasSSHClientHostKey bool `json:"has_ssh_client_host_key"`
	// API_URL advertise to clients
	ApiURL string `json:"api_url" example:"https://api.johnwick.org"`
	// The GRPC_URL advertise to clients
	GrpcURL string `json:"grpc_url" example:"127.0.0.1:8009"`
	// The tenancy type
	TenancyType string `json:"tenancy_type" enums:"selfhosted,multitenant"`
	// License information
	LicenseInfo *ServerLicenseInfo `json:"license_info"`
	// The provider name identified based on the configured identity provider credentials
	IdpProviderName IdpProviderNameType `json:"idp_provider_name"`
	// Indicates if session download functionality is disabled
	// * true - Session download is disabled and not available to users
	// * false - Session download is enabled and available to users
	DisableSessionsDownload bool `json:"disable_sessions_download"`
	// Indicates if clipboard copy functionality is disabled
	// * true - Clipboard copy and cut are disabled and not available to users
	// * false - Clipboard copy and cut are enabled and available to users
	DisableClipboardCopyCut bool `json:"disable_clipboard_copy_cut"`
	// Indicates if all tracking and analytics should be enabled or disabled
	// * enabled - Analytics/tracking are enabled (ANALYTICS_TRACKING=enabled)
	// * disabled - Analytics/tracking are disabled (ANALYTICS_TRACKING=disabled)
	AnalyticsTracking string `json:"analytics_tracking" enums:"enabled,disabled" example:"enabled"`
}

type LivenessCheck struct {
	Liveness string `json:"liveness" enums:"ERR,OK" example:"OK"`
}

type JiraIntegrationStatus string

const (
	JiraIntegrationStatusActive   JiraIntegrationStatus = "enabled"
	JiraIntegrationStatusInactive JiraIntegrationStatus = "disabled"
)

// JiraIntegration represents the Jira integration for an organization
type JiraIntegration struct {
	// The unique identifier of the integration
	ID string `json:"id,omitempty"`

	// The organization identifier
	OrgID string `json:"org_id,omitempty"`

	// The URL of the Jira instance
	URL string `json:"url" binding:"required"`

	// The username for Jira authentication
	User string `json:"user" binding:"required"`

	// The API token for Jira authentication
	APIToken string `json:"api_token" binding:"required"`

	// Report if the integration is enabled or disabled
	Status JiraIntegrationStatus `json:"status"`

	// The creation date and time of the integration
	CreatedAt time.Time `json:"created_at,omitempty"`

	// The last update date and time of the integration
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type JiraIssueTemplate struct {
	// The unique identifier of the integration
	ID string `json:"id"`
	// The name of the template
	Name string `json:"name"`
	// The description of the template
	Description string `json:"description"`
	// The project key which is the shortand version of the project's name
	ProjectKey string `json:"project_key"`
	// The name of the issue transition to change the state of the issue
	// when the session closes
	IssueTransitionNameOnClose string `json:"issue_transition_name_on_close" example:"done"`
	// The request type id that will be associated to the issue
	RequestTypeID string         `json:"request_type_id"`
	MappingTypes  map[string]any `json:"mapping_types"`
	PromptTypes   map[string]any `json:"prompt_types"`
	CmdbTypes     map[string]any `json:"cmdb_types"`
	// The time when the template was created
	CreatedAt time.Time `json:"created_at"`
	// The time when the template was updated
	UpdatedAt time.Time `json:"updated_at"`
	// The connection IDs associated with this template
	ConnectionIDs []string `json:"connection_ids"`
}

type JiraIssueTemplateRequest struct {
	// The name of the template
	Name string `json:"name" binding:"required"`
	// The description of the template
	Description string `json:"description"`
	// The project key which is the shortand version of the project's name
	ProjectKey string `json:"project_key" binding:"required"`
	// The request type that will be associated to the issue
	RequestTypeID string `json:"request_type_id" binding:"required"`
	// The name of the issue transition to change the state of the issue
	// when the session closes
	IssueTransitionNameOnClose string `json:"issue_transition_name_on_close" default:"done"`
	// The automated fields that will be sent when creating the issue.
	// There're two types
	// - preset: obtain the value from a list of available fields that could be propagated
	// The list of available preset values are:
	/*
		- session.id
		- session.user_email
		- session.user_id
		- session.user_name
		- session.type
		- session.connection_subtype
		- session.connection
		- session.connection_tags.[key1]
		- session.connection_tags.[key2]
		- session.status
		- session.script
		- session.start_date
	*/
	// - custom: use a custom static value
	/*
		{
		  "items": [
		    {
		      "description": "Hoop Connection Name",
		      "jira_field": "customfield_10050",
		      "type": "preset",
		      "value": "session.connection"
		    }
		  ]
		}
	*/
	MappingTypes map[string]any `json:"mapping_types"`
	// The prompt fields that will be show to user before executing a session
	/*
		{
		  "items": [
		    {
		      "description": "Squad Name",
		      "jira_field": "customfield_10052",
			  "field_type": "text|select|datetime-local",
		      "label": "Squad Name",
		      "required": true
		    }
		  ]
		}
	*/
	PromptTypes map[string]any `json:"prompt_types"`
	// Cmdb Types are custom fields integrated with the Jira Assets API
	/*
		{
		  "items": [
		    {
		      "description": "Service Field",
		      "jira_field": "customfield_10110",
		      "jira_object_type": "Service",
		      "required": true,
		      "value": "mydb-prod"
		    }
		  ]
		}
	*/
	CmdbTypes map[string]any `json:"cmdb_types"`
	// The connection IDs to associate with this template
	ConnectionIDs []string `json:"connection_ids"`
}

type JiraAssetObjectValue struct {
	// The object identifier
	ID string `json:"id" example:"c1ee84ab-76c8-40d9-a956-13a705d4e605:11013"`
	// Name of the object value
	Name string `json:"name" example:"mycomputer-asset"`
}

type JiraAssetObjects struct {
	// The object values found
	Values []JiraAssetObjectValue `json:"values"`
	// Total amount of records found
	Total int64 `json:"total" example:"22"`
	// Indicate if it has more items
	HasNextPage bool `json:"has_next_page"`
}

type GuardRailRuleRequest struct {
	// Unique name for the rule
	Name string `json:"name" binding:"required" example:"my-strict-rule"`
	// The rule description
	Description string `json:"description" example:"description about this rule"`

	// The input rule
	/*
		{
			"name": "deny-select",
			"description": "<optional-description>",
			"input": {
				"rules": [
					{"type": "deny_words_list", "words": ["SELECT"], "pattern_regex": ""}
				]
			},
			"output": {
				"rules": [
					{"type": "pattern_match", "words": [], "pattern_regex": "[A-Z0-9]+"}
				]
			}
		}
	*/
	Input map[string]any `json:"input"`
	// The output rule
	/*
		{
			"name": "deny-select",
			"description": "<optional-description>",
			"input": {
				"rules": [
					{"type": "deny_words_list", "words": ["SELECT"], "pattern_regex": ""}
				]
			},
			"output": {
				"rules": [
					{"type": "pattern_match", "words": [], "pattern_regex": "[A-Z0-9]+"}
				]
			}
		}
	*/
	Output map[string]any `json:"output"`

	// List of connection IDs that this guardrail applies to
	ConnectionIDs []string `json:"connection_ids" example:"15B5A2FD-0706-4A47-B1CF-B93CCFC5B3D7,15B5A2FD-0706-4A47-B1CF-B93CCFC5B3D8"`
}

type GuardRailRuleResponse struct {
	// The resource identifier
	ID string `json:"id" format:"uuid" readonly:"true" example:"15B5A2FD-0706-4A47-B1CF-B93CCFC5B3D7"`
	// Unique name for the rule
	Name string `json:"name" example:"my-strict-rule"`
	// The rule description
	Description string `json:"description" example:"description about this rule"`

	// The input rule
	/*
		{
			"name": "deny-select",
			"description": "<optional-description>",
			"input": {
				"rules": [
					{"type": "deny_words_list", "words": ["SELECT"], "pattern_regex": "", "name": "<optional-name>"}
				]
			},
			"output": {
				"rules": [
					{"type": "pattern_match", "words": [], "pattern_regex": "[A-Z0-9]+"}
				]
			}
		}
	*/
	Input map[string]any `json:"input"`
	// The output rule
	/*
		{
			"name": "deny-select",
			"description": "<optional-description>",
			"input": {
				"rules": [
					{"type": "deny_words_list", "words": ["SELECT"], "pattern_regex": "", "name": "<optional-name>"}
				]
			},
			"output": {
				"rules": [
					{"type": "pattern_match", "words": [], "pattern_regex": "[A-Z0-9]+"}
				]
			}
		}
	*/
	Output map[string]any `json:"output"`

	// List of connection IDs that this guardrail applies to
	ConnectionIDs []string `json:"connection_ids" example:"15B5A2FD-0706-4A47-B1CF-B93CCFC5B3D7,15B5A2FD-0706-4A47-B1CF-B93CCFC5B3D8"`

	// The time the resource was created
	CreatedAt time.Time `json:"created_at" readonly:"true" example:"2024-07-25T15:56:35.317601Z"`
	// The time the resource was updated
	UpdatedAt time.Time `json:"updated_at" readonly:"true" example:"2024-07-25T15:56:35.317601Z"`
}

// Connection Schema Response is the response for the connection schema
type ConnectionSchemaResponse struct {
	Schemas []ConnectionSchema `json:"schemas"`
	// The output of the connection schema
	/* Example:
		{
			"schemas": [
				{
					"name": "public",
					"tables": [
						{
							"name": "users",
							"columns": [
								{
									"name": "id",
									"type": "integer",
									"nullable": false,
								},
							],
	          }
	        ]
	      }
			]
	} */
}

type ConnectionDatabaseListResponse struct {
	Databases []string `json:"databases"`
}

type ConnectionSchema struct {
	Name   string            `json:"name"`
	Tables []ConnectionTable `json:"tables"` // The tables of the schema
}

type ConnectionTable struct {
	Name    string             `json:"name"`    // The name of the table
	Columns []ConnectionColumn `json:"columns"` // The columns of the table
}

type ConnectionColumn struct {
	Name     string `json:"name"`     // The name of the column
	Type     string `json:"type"`     // The type of the column
	Nullable bool   `json:"nullable"` // The nullable of the column
}

type IAMAccessKeyRequest struct {
	// The AWS access Key ID
	AccessKeyID string `json:"access_key_id" example:"AKIAIOSFODNN7EXAMPLE"`
	// The AWS Secret Access Key. This attribute is required if access_key_id is set
	SecretAccessKey string `json:"secret_access_key" example:"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"`
	// The region that is going to be used by the key or when using instance profile IAM role
	Region string `json:"region" binding:"required" example:"us-west-2"`
	// The session token
	SessionToken string `json:"session_token" example:"AQoEXAMPLEH4aoAH0gNCAPyJxz4BlCFFxWNE1OPTgk5TthT+FvwqnKwRcOIfrRh3c/LTo6UDdyJwOOvEVPvLXCrrrUtdnniCEXAMPLE/IvU1dYUg2RVAJBanLiHb4IgRmpRV3zrkuWJOgQs8IZZaIv2BXIa2R4Olgk"`
}

type IAMUserInfo struct {
	// AccountID is the unique identifier for the AWS account
	AccountID string `json:"account_id" example:"123456789012"`
	// ARN is the Amazon Resource Name that uniquely identifies the IAM user
	ARN string `json:"arn" example:"arn:aws:iam::123456789012:user/johndoe"`
	// UserID is the unique identifier for the IAM user
	UserID string `json:"arn_id" example:"AIDACKCEVSQ6C2EXAMPLE"`
	// Region is the AWS region where the IAM user is operating
	Region string `json:"region" example:"us-west-2"`
}

type IAMEvaluationDetailStatement struct {
	// SourcePolicyID is the unique identifier for the policy
	SourcePolicyID string `json:"source_policy_id" example:"ANPAI3R4QMYGV2EXAMPL4"`
	// SourcePolicyType indicates the type of policy (managed, inline, etc.)
	SourcePolicyType string `json:"source_policy_type" example:"managed"`
}

type IAMEvaluationDetail struct {
	// ActionName is the AWS service action being evaluated
	ActionName string `json:"action_name" example:"ec2:DescribeInstances"`
	// Decision indicates whether the action is allowed or denied
	Decision iamtypes.PolicyEvaluationDecisionType `json:"decision" example:"allowed"`
	// ResourceName is the ARN of the resource being accessed
	ResourceName string `json:"resource_name" example:"arn:aws:ec2:us-west-2:123456789012:instance/i-0123456789abcdef0"`
	// MatchedStatements lists the policy statements that matched during evaluation
	MatchedStatements []IAMEvaluationDetailStatement `json:"matched_statements"`
}

type IAMVerifyPermission struct {
	// Status indicates the overall result of the permission verification
	Status string `json:"status" example:"allowed"`
	// Identity contains information about the IAM user being evaluated
	Identity IAMUserInfo `json:"identity"`
	// EvaluationDetails contains the details of each permission evaluation
	EvaluationDetails []IAMEvaluationDetail `json:"evaluation_details"`
}

type ListAWSAccounts struct {
	Items []AWSAccount `json:"items"`
}

type AWSAccount struct {
	// AccountID is the unique identifier for the AWS account
	AccountID string `json:"account_id" example:"123456789012"`
	// Name is the friendly name of the AWS account
	Name string `json:"name" example:"SandBox"`
	// Status indicates whether the account is active, suspended, etc.
	Status orgtypes.AccountStatus `json:"status" example:"ACTIVE"`
	// JoinedMethods indicates how the account joined the organization
	JoinedMethods orgtypes.AccountJoinedMethod `json:"joined_methods" example:"INVITED"`
	// Email is the email address associated with the AWS account
	Email string `json:"email" example:"aws-prod@example.com"`
}

type ListAWSDBInstancesRequest struct {
	// List of account IDs to scope resources in
	AccountIDs []string `json:"account_ids"`
}

type ListAWSDBInstances struct {
	Items []AWSDBInstance `json:"items"`
}

// AWSDBInstance contains information about an AWS database instance
type AWSDBInstance struct {
	// AccountID is the unique identifier for the AWS account that owns the database
	AccountID string `json:"account_id" example:"123456789012"`
	// Name is the identifier for the database instance
	Name string `json:"name" example:"my-postgres-db"`
	// AvailabilityZone is the AWS availability zone where the database is deployed
	AvailabilityZone string `json:"availability_zone" example:"us-west-2a"`
	// VpcID is the ID of the Virtual Private Cloud where the database is deployed
	VpcID string `json:"vpc_id" example:"vpc-0123456789abcdef0"`
	// ARN is the Amazon Resource Name that uniquely identifies the database instance
	ARN string `json:"arn" example:"arn:aws:rds:us-west-2:123456789012:db:my-postgres-db"`
	// Engine is the database engine type (e.g., MySQL, PostgreSQL)
	Engine string `json:"engine" example:"postgres"`
	// Status indicates the current state of the database instance
	Status string `json:"status" example:"available"`
	// Contains the connection resources that were already provisioned.
	// The resources are marked with a specific tag after completion.
	ConnectionResources []string `json:"connection_resources" example:"pgtest1,pgtest2"`
	// Contains an error in case it was not able to list the db instances from the account id
	Error *string `json:"error" example:"IAM account does not have permission to list db instances in this account"`
}

type CreateDBRoleJobAWSProviderSG struct {
	// The ingress inbound CIDR rule to allow traffic to
	IngressCIDR string `json:"ingress_cidr" example:"192.168.1.0/24" binding:"required"`
}

type CreateDBRoleJobAWSProvider struct {
	// Instance ARN is the identifier for the database instance
	InstanceArn string `json:"instance_arn" binding:"required" example:"arn:aws:rds:us-west-2:123456789012:db:my-instance"`
	// The default security group that will be used to grant access for the agent to access.
	DefaultSecurityGroup *CreateDBRoleJobAWSProviderSG `json:"default_security_group"`
}

type DBRoleJobStepType string

const (
	DBRoleJobStepCreateConnections DBRoleJobStepType = "create-connections"
	DBRoleJobStepSendWebhook       DBRoleJobStepType = "send-webhook"
)

type DBRoleJobVaultProvider struct {
	// The path to store the credentials in Vault
	SecretID string `json:"secret_id" example:"dbsecrets/data/" binding:"required"`
}

type CreateDBRoleJob struct {
	// Unique identifier of the agent hosting the database resource
	AgentID string `json:"agent_id" format:"uuid" binding:"required,min=36" example:"a1b2c3d4-e5f6-7890-abcd-ef1234567890"`
	// Base prefix for connection names - the role name will be appended to this prefix
	// when creating the database connection (e.g., "prod-postgres-ro")
	ConnectionPrefixName string `json:"connection_prefix_name" binding:"required" example:"prod-postgres-"`
	// The additional steps to execute
	JobSteps []DBRoleJobStepType `json:"job_steps" binding:"required,dive,db_role_job_step" example:"create-connections,send-webhook"`
	// Vault Provider uses HashiCorp Vault to store the provisioned credentials.
	// The target agent must be configured with the Vault Credentials in order for this operation to work
	VaultProvider *DBRoleJobVaultProvider `json:"vault_provider"`
	// AWS-specific configuration for the database role creation job
	AWS *CreateDBRoleJobAWSProvider `json:"aws" binding:"required"`
}

type CreateDBRoleJobResponse struct {
	// Unique identifier for the asynchronous job that will create the database role
	JobID string `json:"job_id" example:"8F680C64-DBFD-48E1-9855-6650D9CAD62C"`
}

type DBRoleJob struct {
	// Unique identifier of the organization that owns this job
	OrgID string `json:"org_id" example:"37EEBC20-D8DF-416B-8AC2-01B6EB456318"`
	// Unique identifier for this database role job
	ID string `json:"id" example:"67D7D053-3CAF-430E-97BA-6D4933D3FD5B"`
	// Timestamp when this job was initially created
	CreatedAt time.Time `json:"created_at" example:"2025-02-28T12:34:56Z"`
	// Timestamp when this job finished execution (null if still in progress)
	CompletedAt *time.Time `json:"completed_at" example:"2025-02-28T13:45:12Z"`
	// AWS-specific configuration details for the database role provisioning
	Spec AWSDBRoleJobSpec `json:"spec"`
	// Current status and results of the job execution (null if not started)
	Status *DBRoleJobStatus `json:"status"`
	// The Runbook execution status
	HookStatus *DBRoleJobHookStatus `json:"hook_status"`
}

type DBTag struct {
	Key   string `json:"key" example:"squad"`
	Value string `json:"value" example:"banking"`
}

type AWSDBRoleJobSpec struct {
	// AWS IAM ARN with permissions to execute this role creation job
	AccountArn string `json:"account_arn" example:"arn:aws:iam:123456789012"`
	// ARN of the target RDS database instance where roles will be created
	DBArn string `json:"db_arn" example:"arn:aws:rds:us-west-2:123456789012:db:my-instance"`
	// Logical database name within the RDS instance where roles will be applied
	DBName string `json:"db_name" example:"customers"`
	// Database engine type (e.g., "postgres", "mysql") of the RDS instance
	DBEngine string `json:"db_engine" example:"postgres"`
	// Database Instance tags
	DBTags []DBTag `json:"db_tags"`
}

type DBRoleJobStatus struct {
	// Current execution phase of the job: "running", "failed", or "completed"
	Phase string `json:"phase" enums:"running,failed,completed" example:"running"`
	// Human-readable description of the overall job status or error details
	Message string `json:"message" example:"All user roles have been successfully provisioned"`
	// Detailed results for each individual role that was provisioned
	Result []DBRoleJobStatusResult `json:"result"`
}

type DBRoleJobHookStatus struct {
	// The Unix Exit Code of the runbook execution (0=success)
	ExitCode int `json:"exit_code" example:"0"`
	// The stdout and stderr streams captured during execution
	Output string `json:"output" example:"output"`
	// The time it took to execute the runbook
	ExecutionTimeSec int `json:"execution_time_sec" example:"5"`
}

type CreateRdsRootPasswordRequest struct {
	// the instance arn id to generate credentials for
	InstanceArnItems []string `json:"instances" binding:"required" example:"db-arn-1,db-arn-2"`
}

type CreateRdsRootPasswordResponse struct {
	// The passwords generated by each instance
	Credentials map[string]CreateRdsRootPasswordCredentialsInfo `json:"credentials"`
}

type CreateRdsRootPasswordCredentialsInfo struct {
	// The random password generated for the instance.
	Password string `json:"password" example:"random-password"`
	// The time when this password will expire, another random password will be generated after this time and this password will no longer work.
	ExpireAt time.Time `json:"expire_at" example:"2025-02-28T12:34:56Z"`
}

type SecretsManagerProviderType string

const (
	SecretsManagerProviderDatabase SecretsManagerProviderType = "database"
	SecretsManagerProviderVault    SecretsManagerProviderType = "vault"
)

type DBRoleJobStatusResultCredentialsInfo struct {
	// The secrets manager provider that was used to store the credentials
	SecretsManagerProvider SecretsManagerProviderType `json:"secrets_manager_provider" example:"database"`
	// The secret identifier that contains the secret data.
	// This value is always empty for the database type.
	SecretID string `json:"secret_id" example:"dbsecrets/data/pgprod"`
	// The keys that were saved in the secrets manager.
	// This value is always empty for the database type.
	SecretKeys []string `json:"secret_keys" example:"HOST,PORT,USER,PASSWORD,DB"`
}

type DBRoleJobStatusResult struct {
	// Name of the specific database role that was provisioned
	UserRole string `json:"user_role" example:"hoop_ro"`
	// Status of this specific role's provisioning: "running", "failed", or "completed"
	Status string `json:"status" enums:"running,failed,completed" example:"failed"`
	// Human-readable description of this role's provisioning status or error details
	Message string `json:"message" example:"process already being executed, resource_id=arn:aws:rds:us-west-2:123456789012:db:my-postgres-db"`
	// Credentials information about the stored secrets
	CredentialsInfo DBRoleJobStatusResultCredentialsInfo `json:"credentials_info"`
	// Timestamp when this specific role's provisioning completed
	CompletedAt time.Time `json:"completed_at" example:"2025-02-28T12:34:56Z"`
}

type DBRoleJobList struct {
	Items []DBRoleJob `json:"items"`
}

// SchemaInfo represents information about a database schema and its tables
type SchemaInfo struct {
	Name   string   `json:"name"`   // The name of the schema
	Tables []string `json:"tables"` // The tables in the schema
}

// TablesResponse represents the response for a tables list request
type TablesResponse struct {
	Schemas []SchemaInfo `json:"schemas"` // The database schemas
}

// ColumnsResponse represents the response for a table columns request
type ColumnsResponse struct {
	Columns []ConnectionColumn `json:"columns"` // The columns of a table
}

type SessionMetricResponse struct {
	SessionID          string     `json:"session_id" format:"uuid" example:"1CBC8DB5-FBF8-4293-8E35-59A6EEA40207"`
	OrgID              string     `json:"org_id" format:"uuid" example:"0CD7F941-2BB8-4F9F-93B0-11620D4652AB"`
	ConnectionType     string     `json:"connection_type" example:"postgres"`
	ConnectionSubtype  *string    `json:"connection_subtype" example:"amazon-rds"`
	ConnectionName     string     `json:"connection_name" example:"production-db"`
	InfoType           string     `json:"info_type" example:"EMAIL_ADDRESS"`
	CountMasked        int64      `json:"count_masked" example:"15"`
	CountAnalyzed      int64      `json:"count_analyzed" example:"20"`
	IsMasked           bool       `json:"is_masked" example:"true"`
	SessionCreatedAt   time.Time  `json:"session_created_at" example:"2023-08-15T14:30:45Z"`
	SessionEndedAt     *time.Time `json:"session_ended_at" example:"2023-08-15T14:35:10Z"`
	SessionDurationSec *int       `json:"session_duration_sec" example:"325"`
}

type SessionMetricsAggregatedResponse struct {
	TotalSessions         int64  `json:"total_sessions" example:"150"`
	UniqueInfoTypes       int64  `json:"unique_info_types" example:"12"`
	TotalMasked           int64  `json:"total_masked" example:"5420"`
	TotalAnalyzed         int64  `json:"total_analyzed" example:"7850"`
	SessionsWithMasking   int64  `json:"sessions_with_masking" example:"98"`
	AvgSessionDurationSec *int64 `json:"avg_session_duration_sec" example:"285"`
}

type DataMaskingRule struct {
	// The unique identifier of the data masking rule
	ID                     string `json:"id" format:"uuid" example:"15B5A2FD-0706-4A47-B1CF-B93CCFC5B3D7"`
	DataMaskingRuleRequest `json:",inline"`
}

type DataMaskingRuleRequest struct {
	// The unique name of the data masking rule, it's immutable after creation
	Name string `json:"name" binding:"required" example:"mask-email"`
	// The description of the data masking rule
	Description string `json:"description" example:"Mask email addresses in the data"`
	// The connections that this rule applies to
	ConnectionIDs []string `json:"connection_ids" example:"15B5A2FD-0706-4A47-B1CF-B93CCFC5B3D7,15B5A2FD-0706-4A47-B1CF-B93CCFC5B3D8"`
	// The registered entity types that this rule applies to
	SupportedEntityTypes []SupportedEntityTypesEntry `json:"supported_entity_types"`
	// The minimal detection score threshold for the entities to be masked.
	ScoreThreshold *float64 `json:"score_threshold" example:"0.6"`
	// The custom entity types that this rule applies to
	CustomEntityTypesEntrys []CustomEntityTypesEntry `json:"custom_entity_types"`
	// The timestamp when the rule was updated
	UpdatedAt time.Time `json:"updated_at" readonly:"true" example:"2023-08-15T14:30:45Z"`
}

type SupportedEntityTypesEntry struct {
	// An identifier for this structure, it's used as an identifier of a collection of entities.
	Name string `json:"name" example:"PII"`
	// The registered entity types in the redact provider
	EntityTypes []string `json:"entity_types" example:"EMAIL_ADDRESS,PERSON,PHONE_NUMBER,IP_ADDRESS"`
}

type CustomEntityTypesEntry struct {
	// The name of the custom entity type as uppercase
	Name string `json:"name" binding:"required" example:"ZIP_CODE"`
	// The regex pattern to match (python) the custom entity type.
	// Either this or the deny_list is required
	Regex string `json:"regex" example:"\\b\\d{5}(?:-\\d{4})?\\b"`
	// List of words to be returned as PII if found.
	// Either this or the regex is required
	DenyList []string `json:"deny_list" example:"Mr,Mr.,Mister"`
	// Detection confidence of this pattern (0.01 if very noisy, 0.6-1.0 if very specific)
	Score float64 `json:"score" binding:"required" example:"0.01"`
}

type DataMaskingRuleConnectionRequest struct {
	// The unique identifier of the data masking rule
	RuleID string `json:"rule_id" example:"15B5A2FD-0706-4A47-B1CF-B93CCFC5B3D7"`
	// The status of the data masking rule
	Status string `json:"status" enums:"active,inactive" example:"active" binding:"required"`
}

type DataMaskingRuleConnection struct {
	// The unique identifier of the data masking rule connection
	ID string `json:"id" example:"15B5A2FD-0706-4A47-B1CF-B93CCFC5B3D7"`
	// The unique identifier of the data masking rule
	RuleID string `json:"rule_id" example:"15B5A2FD-0706-4A47-B1CF-B93CCFC5B3D7"`
	// The unique identifier of the connection
	ConnectionID string `json:"connection_id" example:"15B5A2FD-0706-4A47-B1CF-B93CCFC5B3D7"`
	// The status of the data masking rule connection
	Status string `json:"status" enums:"active,inactive" example:"active"`
}

type ServerMiscConfig struct {
	// Either to enable or disable the product analytics tracking
	ProductAnalytics string `json:"product_analytics" enum:"active,inactive" example:"active"`
	// The gRPC server URL used to advertise the gRPC server to clients
	GrpcServerURL string `json:"grpc_server_url" default:"grpc://127.0.0.1:8010"`
	// The PostgreSQL server proxy configuration
	PostgresServerConfig *PostgresServerConfig `json:"postgres_server_config"`
	// The SSH server proxy configuration
	SSHServerConfig *SSHServerConfig `json:"ssh_server_config"`
	// The RDP server proxy configuration
	RDPServerConfig *RDPServerConfig `json:"rdp_server_config"`
	// The HTTP proxy server configuration
	HttpProxyServerConfig *HttpProxyServerConfig `json:"http_proxy_server_config"`
}

type SSHServerConfig struct {
	// The listen address to run the SSH server proxy
	ListenAddress string `json:"listen_address" example:"0.0.0.0:12222"`
	// The hosts key used for SSH connections
	HostsKey string `json:"hosts_key" example:"base64-pem-encoded-hosts-key"`
}

type RDPServerConfig struct {
	// The listen address to run the RDP server proxy
	ListenAddress string `json:"listen_address" example:"0.0.0.0:3389"`
}

type PostgresServerConfig struct {
	// The listen address to run the PostgreSQL server proxy
	ListenAddress string `json:"listen_address" example:"0.0.0.0:15432"`
}

type HttpProxyServerConfig struct {
	// The HTTP proxy server URL
	ListenAddress string `json:"listen_address" example:"http://0.0.0.0:18888"`
}

type ServerAuthOidcConfig struct {
	// Identity Provider Issuer URL (Oauth2)
	IssuerURL string `json:"issuer_url" example:"https://auth.domain.tld/oidc" binding:"required"`
	// Oauth2 Client ID
	ClientID string `json:"client_id" example:"hoop-client-id" binding:"required"`
	// Oauth2 Client Secret
	ClientSecret string `json:"client_secret" example:"hoop-client-secret" binding:"required"`
	// Additional Oauth2 scopes to append in the request. Default values are openid, profile and email.
	Scopes []string `json:"scopes" example:"openid,email,profile"`
	// Identity Provider Audience (Oauth2)
	Audience string `json:"audience" example:"hoop-audience"`
	// Specifies the claim identifier used to configure group propagation.
	GroupsClaim string `json:"groups_claim" example:"groups"`
}

type ServerAuthSamlConfig struct {
	// Identity Provider Metadata URL (SAML 2.0)
	IdpMetadataURL string `json:"idp_metadata_url" example:"https://auth.domain.tld/saml/metadata" binding:"required"`
	// Specifies the claim identifier used to configure group propagation.
	GroupsClaim string `json:"groups_claim" default:"groups"`
}

type ProviderType string

const (
	ProviderTypeOIDC  ProviderType = "oidc"
	ProviderTypeSAML  ProviderType = "saml"
	ProviderTypeLocal ProviderType = "local"
)

type ServerAuthConfig struct {
	// The identity provider type to configure
	AuthMethod ProviderType `json:"auth_method" example:"local" binding:"required"`
	// OIDC / Oauth2 identity provider configuration
	OidcConfig *ServerAuthOidcConfig `json:"oidc_config"`
	// SAML 2.0 identity provider configuration
	SamlConfig *ServerAuthSamlConfig `json:"saml_config"`
	// The provider type name used to identify the authentication provider
	ProviderName string `json:"provider_name" example:"generic"`
	// The api key with admin privileges used to authenticate in the API. It is a read only field
	ApiKey *string `json:"api_key" example:"xapi-WqIAoYhKuIv2IPmVkfsyyK" readonly:"true"`
	// The api key to rollout. When this field is set, the server will rollout the previous api_key.
	// This attribute must be obtained in the endpoint to generate rollout api keys.
	RolloutApiKey *string `json:"rollout_api_key" example:"xapi-WqIAoYhKuIv2IPmVkfsyyK"`
	// Enable the users management in the Webapp. It allows to create, edit and delete users.
	WebappUsersManagementStatus string `json:"webapp_users_management_status" enums:"active,inactive" binding:"required"`
	// Changes the default administrator role of the system
	AdminRoleName string `json:"admin_role_name" default:"admin"`
	// Changes the default auditor role of the system
	AuditorRoleName string `json:"auditor_role_name" default:"auditor"`
}

type GenerateApiKeyResponse struct {
	// The API key that was generated to rollout the previous api_key.
	RolloutApiKey string `json:"rollout_api_key" example:"xapi-WqIAoYhKuIv2IPmVkfsyyK"`
}

type LocalUserRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
	Name     string `json:"name"`
}

type ConnectionCredentialsRequest struct {
	AccessDurationSec int `json:"access_duration_seconds"`
}

type ConnectionCredentialsResponse struct {
	// The unique identifier of the connection database access
	ID string `json:"id" format:"uuid" readonly:"true" example:"15B5A2FD-0706-4A47-B1CF-B93CCFC5B3D7"`
	// The name of the connection
	ConnectionName string `json:"connection_name" example:"pgdemo"`
	// Connection type
	ConnectionType string `json:"connection_type" example:"postgres"`
	// The connection subtype
	ConnectionSubType string `json:"connection_subtype" example:"postgres"`
	// The connection information
	ConnectionCredentials any `json:"connection_credentials"`
	// When the database access connection expires
	ExpireAt time.Time `json:"expire_at" example:"2025-08-25T13:00:00Z"`
	// When the resource was created
	CreatedAt time.Time `json:"created_at" example:"2025-08-25T12:00:00Z"`
}

type RDPConnectionInfo struct {
	// The hostname to access the rdp server pinstance
	Hostname string `json:"hostname" example:"example.com/198.22.2.2"`
	// The port of the rdp server instance
	Port string `json:"port" example:"13389"`
	// The username of the rdp server instance
	Username string `json:"username" example:"noop"`
	// The password of the rdp server instance
	Password string `json:"password" example:"noop"`
	// The command to access the rdp instance
	Command string `json:"command" example:"xfreerdp /v:0.0.0.0:13389 /u:fake /p:fake"`
}

// SSMConnectionInfo represents the connection information required to establish
// a session with an AWS Systems Manager (SSM) managed instance.
type SSMConnectionInfo struct {
	// The hostname to access the ssm server instance
	EndpointURL string `json:"endpoint_url" example:"https://hoop.gateway/ssm"`
	// The AWS access key ID for the AWS CLI
	AwsAccessKeyId string `json:"aws_access_key_id" example:"AKIAIOSFODNN7EXAMPLE"`
	// The AWS secret access key for the AWS CLI
	AwsSecretAccessKey string `json:"aws_secret_access_key" example:"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"`
	// Command line arguments to access the ssm server instance
	ConnectionString string `json:"connection_string" example:"aws ssm start-session --target ... --endpoint-url ..."`
}

// HttpProxyConnectionInfo represents the connection information required to establish
type HttpProxyConnectionInfo struct {
	// Hostname to access the HTTP proxy instance
	Hostname string `json:"hostname"`
	// Port of the HTTP proxy instance
	Port string `json:"port"`
	// Token to access the HTTP proxy resource
	ProxyToken string `json:"proxy_token"`
	// The command to access the HTTP proxy instance
	Command string `json:"command"`
}

type PostgresConnectionInfo struct {
	// The hostname to access the database instance
	Hostname string `json:"hostname" example:"db.example.com"`
	// The port of the database instance
	Port string `json:"port" example:"5432"`
	// The username of the database instance
	Username string `json:"username" example:"noop"`
	// The password of the database instance
	Password string `json:"password" example:"noop"`
	// The default database name of the connection
	DatabaseName string `json:"database_name" example:"mydb"`
	// The connection string to access the database instance
	ConnectionString string `json:"connection_string" example:"postgres://noop:noop@db.example.com:5432/mydb?sslmode=disable"`
}

type SSHConnectionInfo struct {
	// The hostname to access the SSH instance
	Hostname string `json:"hostname" example:"ssh.example.com"`
	// The port of the SSH instance
	Port string `json:"port" example:"22"`
	// The username of the SSH instance
	Username string `json:"username" example:"noop"`
	// The password of the SSH instance
	Password string `json:"password" example:"noop"`
	// The command to access the SSH instance
	Command string `json:"command" example:"ssh -p 22"`
}

type Pagination struct {
	Total int `json:"total"`
	Page  int `json:"page"`
	Size  int `json:"size"`
}

type PaginatedResponse[T any] struct {
	Pages Pagination `json:"pages"`
	Data  []T        `json:"data"`
}

type ConnectionSearch struct {
	// Unique ID of the resource
	ID string `json:"id" readonly:"true" format:"uuid" example:"5364ec99-653b-41ba-8165-67236e894990"`
	// Name of the connection. This attribute is immutable when updating it
	Name string `json:"name" binding:"required" example:"pgdemo"`
	// The resource name associated with this connection
	ResourceName string `json:"resource_name" example:"my-resource"`
	// Type represents the main type of the connection:
	// * database - Database protocols
	// * application - Custom applications
	// * custom - Shell applications
	Type string `json:"type" binding:"required" enums:"database,application,custom" example:"database"`
	// Sub Type is the underline implementation of the connection:
	// * postgres - Implements Postgres protocol
	// * mysql - Implements MySQL protocol
	// * mongodb - Implements MongoDB Wire Protocol
	// * mssql - Implements Microsoft SQL Server Protocol
	// * oracledb - Implements Oracle Database Protocol
	// * tcp - Forwards a TCP connection
	// * ssh - Forwards a SSH connection
	// * httpproxy - Forwards a HTTP connection
	// * dynamodb - AWS DynamoDB experimental integration
	// * cloudwatch - AWS CloudWatch experimental integration
	SubType string `json:"subtype" example:"postgres"`
	// Status is a read only field that informs if the connection is available for interaction
	// * online - The agent is connected and alive
	// * offline - The agent is not connected
	Status string `json:"status" readonly:"true" enums:"online,offline"`
	// Toggle Ad Hoc Runbooks Executions
	// * enabled - Enable to run runbooks for this connection
	// * disabled - Disable runbooks execution for this connection
	AccessModeRunbooks string `json:"access_mode_runbooks" binding:"required" enums:"enabled,disabled"`
	// Toggle Ad Hoc Executions
	// * enabled - Enable to run ad-hoc executions for this connection
	// * disabled - Disable ad-hoc executions for this connection
	AccessModeExec string `json:"access_mode_exec" binding:"required" enums:"enabled,disabled"`
	// Toggle Port Forwarding
	// * enabled - Enable to perform port forwarding for this connection
	// * disabled - Disable port forwarding for this connection
	AccessModeConnect string `json:"access_mode_connect" binding:"required" enums:"enabled,disabled"`
}

type ResourceSearch struct {
	// Unique ID of the resource
	ID string `json:"id" readonly:"true" format:"uuid" example:"5364ec99-653b-41ba-8165-67236e894990"`
	// Name of the connection. This attribute is immutable when updating it
	Name string `json:"name" binding:"required" example:"pgdemo"`
	// Type represents the main type of the connection:
	// * database - Database protocols
	// * application - Custom applications
	// * custom - Shell applications
	Type string `json:"type" binding:"required" enums:"database,application,custom" example:"database"`
	// Sub Type is the underline implementation of the connection:
	// * postgres - Implements Postgres protocol
	// * mysql - Implements MySQL protocol
	// * mongodb - Implements MongoDB Wire Protocol
	// * mssql - Implements Microsoft SQL Server Protocol
	// * oracledb - Implements Oracle Database Protocol
	// * tcp - Forwards a TCP connection
	// * ssh - Forwards a SSH connection
	// * httpproxy - Forwards a HTTP connection
	// * dynamodb - AWS DynamoDB experimental integration
	// * cloudwatch - AWS CloudWatch experimental integration
	SubType string `json:"subtype" example:"postgres"`
}

type RunbookSearch struct {
	// Repository name
	Repository string `json:"repository" example:"github.com/myorg/myrunbooks"`
	// The runbook name
	Name string `json:"name" example:"myrunbooks/run-backup.runbook.sql"`
}

type SearchResponse struct {
	// Any errors found during the search
	Errors []string `json:"errors"`
	// Connections found in the search
	Connections []ConnectionSearch `json:"connections"`
	// Runbooks found in the search
	Runbooks []*RunbookSearch `json:"runbooks"`
	// Resources found in the search
	Resources []ResourceSearch `json:"resources"`
}

type ConnectionTestResponse struct {
	// Indicates if the connection test was successful
	Success bool `json:"success" example:"true"`
}

type ResourceRoleRequest struct {
	// Name of the connection. This attribute is immutable when updating it
	Name string `json:"name" binding:"required" example:"pgdemo"`
	// Is the shell command that is going to be executed when interacting with this connection.
	// This value is required if the connection is going to be used from the Webapp.
	Command []string `json:"command" example:"/bin/bash"`
	// Type represents the main type of the connection:
	// * database - Database protocols
	// * application - Custom applications
	// * custom - Shell applications
	Type string `json:"type" binding:"required" enums:"database,application,custom" example:"database"`
	// Sub Type is the underline implementation of the connection:
	// * postgres - Implements Postgres protocol
	// * mysql - Implements MySQL protocol
	// * mongodb - Implements MongoDB Wire Protocol
	// * mssql - Implements Microsoft SQL Server Protocol
	// * oracledb - Implements Oracle Database Protocol
	// * tcp - Forwards a TCP connection
	// * ssh - Forwards a SSH connection
	// * httpproxy - Forwards a HTTP connection
	// * dynamodb - AWS DynamoDB experimental integration
	// * cloudwatch - AWS CloudWatch experimental integration
	SubType string `json:"subtype" example:"postgres"`
	// Secrets are environment variables that are going to be exposed
	// in the runtime of the connection:
	// * { envvar:[env-key]: [base64-val] } - Expose the value as environment variable
	// * { filesystem:[env-key]: [base64-val] } - Expose the value as a temporary file path creating the value in the filesystem
	//
	// The value could also represent an integration with a external provider:
	// * { envvar:[env-key]: _aws:[secret-name]:[secret-key] } - Obtain the value dynamically in the AWS secrets manager and expose as environment variable
	// * { envvar:[env-key]: _envjson:[json-env-name]:[json-env-key] } - Obtain the value dynamically from a JSON env in the agent runtime. Example: MYENV={"KEY": "val"}
	Secrets map[string]any `json:"secret"`
	// The agent associated with this connection
	AgentID string `json:"agent_id" format:"uuid" example:"1837453e-01fc-46f3-9e4c-dcf22d395393"`
}

type ResourceRequest struct {
	// The resource name
	Name string `json:"name" binding:"required" example:"my-resource"`
	// The resource type
	Type string `json:"type" binding:"required" example:"database"`
	// The resource subtype
	SubType string `json:"subtype" binding:"required" example:"mysql"`
	// The resource environment variables
	EnvVars map[string]string `json:"env_vars" binding:"required"`
	// The agent associated with this resource
	AgentID string `json:"agent_id" format:"uuid" example:"1837453e-01fc-46f3-9e4c-dcf22d395393"`
	// The roles associated with this resource
	Roles []ResourceRoleRequest `json:"roles" binding:"dive"`
}

type ResourceResponse struct {
	// The resource ID
	ID string `json:"id" format:"uuid" readonly:"true" example:"15B5A2FD-0706-4A47-B1CF-B93CCFC5B3D7"`
	// The resource name
	Name string `json:"name" example:"my-resource"`
	// The resource type
	Type string `json:"type" example:"database"`
	// The resource subtype
	SubType string `json:"subtype" example:"mysql"`
	// The resource environment variables
	EnvVars map[string]string `json:"env_vars"`
	// The agent associated with this resource
	AgentID string `json:"agent_id" binding:"required" format:"uuid" example:"1837453e-01fc-46f3-9e4c-dcf22d395393"`
	// The time the resource was created
	CreatedAt time.Time `json:"created_at" readonly:"true" example:"2024-07-25T15:56:35.317601Z"`
	// The time the resource was updated
	UpdatedAt time.Time `json:"updated_at" readonly:"true" example:"2024-07-25T15:56:35.317601Z"`
}

type RunbookConfigurationRequest struct {
	// The runbook repository configuration
	Repositories []RunbookRepository `json:"repositories" binding:"required"`
}

type RunbookConfigurationResponse struct {
	// The unique identifier of the runbook
	ID string `json:"id" format:"uuid" readonly:"true" example:"15B5A2FD-0706-4A47-B1CF-B93CCFC5B3D7"`
	// Organization ID that owns this configuration
	OrgID string `json:"org_id" format:"uuid" readonly:"true" example:"37EEBC20-D8DF-416B-8AC2-01B6EB456318"`
	// The runbook repository configuration
	Repositories []RunbookRepositoryResponse `json:"repositories"`
	// The time the resource was created
	CreatedAt time.Time `json:"created_at" readonly:"true" example:"2024-07-25T15:56:35.317601Z"`
	// The time the resource was updated
	UpdatedAt time.Time `json:"updated_at" readonly:"true" example:"2024-07-25T15:56:35.317601Z"`
}

type RunbookRepository struct {
	// Git repository URL where the runbook is located
	GitUrl string `json:"git_url" binding:"required" example:"https://github.com/myorg/myrepo"`
	// Git username for repository authentication
	GitUser string `json:"git_user" example:"myusername"`
	// Git password or token for repository authentication
	GitPassword string `json:"git_password" example:"mypassword"`
	// SSH private key for Git repository authentication
	SSHKey string `json:"ssh_key" example:"-----BEGIN PRIVATE KEY-----..."`
	// SSH username for Git repository authentication
	SSHUser string `json:"ssh_user" example:"myuser"`
	// SSH key passphrase for encrypted SSH keys
	SSHKeyPass string `json:"ssh_keypass" example:"mykeypassphrase"`
	// Git SSH known hosts for host key verification
	SSHKnownHosts string `json:"ssh_known_hosts" example:"github.com ssh-rsa AAAA..."`
	// Enables runbook hooks when this value is greater than zero
	GitHookTTL int `json:"git_hook_ttl" example:"1000"`
}

type RunbookRepositoryResponse struct {
	// Git repository identifier in the format `host/owner/repo`
	Repository string `json:"repository" readonly:"true" example:"github.com/myorg/myrepo"`
	// Git repository URL where the runbook is located
	GitUrl string `json:"git_url" example:"https://github.com/myorg/myrepo"`
	// Git username for repository authentication
	GitUser string `json:"git_user" example:"myusername"`
	// Git password or token for repository authentication
	GitPassword string `json:"git_password" example:"mypassword"`
	// SSH private key for Git repository authentication
	SSHKey string `json:"ssh_key" example:"-----BEGIN PRIVATE KEY-----..."`
	// SSH username for Git repository authentication
	SSHUser string `json:"ssh_user" example:"myuser"`
	// SSH key passphrase for encrypted SSH keys
	SSHKeyPass string `json:"ssh_keypass" example:"mykeypassphrase"`
	// Git SSH known hosts for host key verification
	SSHKnownHosts string `json:"ssh_known_hosts" example:"github.com ssh-rsa AAAA..."`
	// Enables runbook hooks when this value is greater than zero
	GitHookTTL int `json:"git_hook_ttl" example:"1000"`
}

type RunbookV2 struct {
	// File path relative to repository root containing runbook file in the following format: `/path/to/file.runbook.<ext>`
	Name string `json:"name" example:"ops/update-user.runbook.sh"`
	// Metadata contains the attributes parsed from a template.
	// Payload Example:
	/*
		{
			"customer_id" : {
				"description": "the id of the customer",
				"required": true,
				"type": "text",
				"default": "Default value to use",
				"order": 1
			},
			"country": {
				"description": "the country code US; BR, etc",
				"required": false,
				"type": "select",
				"options": ["US", "BR"],
				"order": 2
			}
		}
	*/
	// By default it will have the attributes `description=""`, `required=false` and `type="text"`.
	// Optional attributes include `order` (int), `default`, `placeholder`, `options`, and `asenv`.
	Metadata map[string]any `json:"metadata"`
	// The connections that could be used for this runbook
	ConnectionList []string `json:"connections,omitempty" example:"pgdemo,bash"`
	// The error description if it failed to render
	Error      *string           `json:"error"`
	EnvVars    map[string]string `json:"-"`
	InputFile  []byte            `json:"-"`
	CommitHash string            `json:"-"`
}

type RunbookRepositoryList struct {
	Items []*Runbook `json:"items"`
	// Repository url
	Repository string `json:"repository" example:"github.com/myorg/myrepo"`
	// The commit sha
	Commit string `json:"commit" example:"03c25fd64c74712c71798250d256d4b859dd5853"`
	// The commit author
	CommitAuthor string `json:"commit_author" example:"John Wick <john.wick@bad.org>"`
	// The commit message
	CommitMessage string `json:"commit_message" example:"runbook update"`
}

type RunbookListV2 struct {
	// Errors encountered during fetching runbooks
	Errors []string `json:"errors"`
	// List of runbook repositories
	Repositories []RunbookRepositoryList `json:"repositories"`
}

type RunbookRuleFile struct {
	// The normalized name of the repository
	Repository string `json:"repository" example:"github.com/myorg/myrepo"`
	// The relative git path of the runbook file
	Name string `json:"name" example:"ops/update-user.runbook.sh"`
}

type RunbookRuleRequest struct {
	// The name of the runbook rule
	Name string `json:"name" binding:"required" example:"Default Runbook Rules"`
	// The description of the runbook rule
	Description string `json:"description" example:"Runbook rules for production databases"`
	// The runbook files associated with this rule
	Runbooks []RunbookRuleFile `json:"runbooks" binding:"required,dive"`
	// The connection names that this rule applies to
	Connections []string `json:"connections" binding:"required" example:"pgdemo,bash"`
	// The user groups names that can access this runbook rule
	UserGroups []string `json:"user_groups" binding:"required" example:"dba-team,devops-team"`
}

type RunbookRule struct {
	// The unique identifier of the runbook rule
	ID string `json:"id" format:"uuid" readonly:"true" example:"15B5A2FD-0706-4A47-B1CF-B93CCFC5B3D7"`
	// Organization ID that owns this runbook rule
	OrgID string `json:"org_id" format:"uuid" readonly:"true" example:"37EEBC20-D8DF-416B-8AC2-01B6EB456318"`
	// The name of the runbook rule
	Name string `json:"name" binding:"required" example:"Default Runbook Rules"`
	// The description of the runbook rule
	Description string `json:"description" example:"Runbook rules for production databases"`
	// The runbook files associated with this rule
	Runbooks []RunbookRuleFile `json:"runbooks" binding:"required,dive"`
	// The connection names that this rule applies to
	Connections []string `json:"connections" binding:"required" example:"pgdemo,bash"`
	// The user groups names that can access this runbook rule
	UserGroups []string `json:"user_groups" binding:"required" example:"dba-team,devops-team"`
	// The time the resource was created
	CreatedAt time.Time `json:"created_at" readonly:"true" example:"2024-07-25T15:56:35.317601Z"`
	// The time the resource was updated
	UpdatedAt time.Time `json:"updated_at" readonly:"true" example:"2024-07-25T15:56:35.317601Z"`
}

type RunbookExec struct {
	// Connection name to execute the runbook against
	ConnectionName string `json:"connection_name" binding:"required" example:"pgdemo"`
	// Repository name where the runbook is located
	Repository string `json:"repository" binding:"required" example:"github.com/myorg/myrepo"`
	// The relative path name of the runbook file from the git source
	FileName string `json:"file_name" binding:"required" example:"myrunbooks/run-backup.runbook.sql"`
	// The commit sha reference to obtain the file
	RefHash string `json:"ref_hash" example:"20320ebbf9fc612256b67dc9e899bbd6e4745c77"`
	// The parameters of the runbook. It must match with the declared attributes
	Parameters map[string]string `json:"parameters" example:"amount:10,wallet_id:6736"`
	// Environment Variables that will be included in the runtime
	// * { envvar:[env-key]: [base64-val] } - Expose the value as environment variable
	// * { filesystem:[env-key]: [base64-val] } - Expose the value as a temporary file path creating the value in the filesystem
	EnvVars map[string]string `json:"env_vars" example:"envvar:PASSWORD:MTIz,filesystem:SECRET_FILE:bXlzZWNyZXQ="`
	// Additional arguments to pass down to the connection
	ClientArgs []string `json:"client_args" example:"--verbose"`
	// Metadata attributes to add in the session
	Metadata map[string]any `json:"metadata"`
	// Jira fields to create a Jira issue
	JiraFields map[string]string `json:"jira_fields"`
	// Batch identifier to group sessions that were executed simultaneously
	SessionBatchID *string `json:"session_batch_id,omitempty" example:"batch-abc-123"`
}

type AccessRequestRule struct {
	// The resource identifier
	ID string `json:"id" format:"uuid" readonly:"true" example:"15B5A2FD-0706-4A47-B1CF-B93CCFC5B3D7"`
	// The name of the access request rule
	Name string `json:"name" example:"default-access-request-rule"`
	// The description of the access request rule
	Description *string `json:"description" example:"Access control rule for production databases"`
	// The access type
	AccessType string `json:"access_type" enums:"jit,command" example:"command"`
	// Connection names that this rule applies to
	ConnectionNames []string `json:"connection_names" example:"pgdemo,mysql-prod"`
	// Groups that require approval
	ApprovalRequiredGroups []string `json:"approval_required_groups" example:"developers,analysts"`
	// Whether all groups must approve
	AllGroupsMustApprove bool `json:"all_groups_must_approve" example:"false"`
	// Groups that can review sessions
	ReviewersGroups []string `json:"reviewers_groups" example:"sre,dba"`
	// Groups that can force approve sessions
	ForceApprovalGroups []string `json:"force_approval_groups" example:"admin"`
	// Maximum access duration in seconds
	AccessMaxDuration *int `json:"access_max_duration" example:"3600"`
	// Minimum number of approvals required
	MinApprovals *int `json:"min_approvals" example:"2"`
	// The time the resource was created
	CreatedAt time.Time `json:"created_at" readonly:"true" example:"2024-07-25T15:56:35.317601Z"`
	// The time the resource was updated
	UpdatedAt time.Time `json:"updated_at" readonly:"true" example:"2024-07-25T15:56:35.317601Z"`
}

type AccessRequestRuleRequest struct {
	// The name of the access request rule
	Name string `json:"name" binding:"required" example:"default-access-request-rule"`
	// The description of the access request rule
	Description *string `json:"description" example:"Access request rule for production databases"`
	// The access type
	AccessType string `json:"access_type" binding:"required" enums:"jit,command" example:"command"`
	// Connection names that this rule applies to
	ConnectionNames []string `json:"connection_names" binding:"required" example:"pgdemo,mysql-prod"`
	// Groups that require approval
	ApprovalRequiredGroups []string `json:"approval_required_groups" binding:"required" example:"developers,analysts"`
	// Whether all groups must approve
	AllGroupsMustApprove bool `json:"all_groups_must_approve" example:"false"`
	// Groups that can review sessions
	ReviewersGroups []string `json:"reviewers_groups" binding:"required" example:"sre,dba"`
	// Groups that can force approve sessions
	ForceApprovalGroups []string `json:"force_approval_groups" binding:"required" example:"admin"`
	// Maximum access duration in seconds
	AccessMaxDuration *int `json:"access_max_duration,omitempty" example:"3600"`
	// Minimum number of approvals required
	MinApprovals *int `json:"min_approvals,omitempty" example:"2"`
}
