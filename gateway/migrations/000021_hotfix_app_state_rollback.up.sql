-- this migration will fix broken state rollback instructions
-- it add the function agents(connections) to the list and it's safe to update all previous
-- record with these instructions

-- if a particular function or view doesn't exist in the state it will
-- be ignored
SET search_path TO private;

update appstate set state_rollback = '# Instructions to remove the current state of postgrest database
# hash tags and spaces are ignored. Only accepts functions and views
# <resource-type> <resource>

function if exists blob_input(reviews)
function if exists blob_input(sessions)
function if exists blob_stream(sessions)
function if exists env_vars(plugin_connections)
function if exists env_vars(plugins)
function if exists groups(serviceaccounts)
function if exists groups(users)
function if exists update_connection(json)
function if exists update_serviceaccounts(uuid,uuid,text,text,private.enum_service_account_status,character varying[])
function if exists update_users(json)
function if exists session_report(json)
function if exists agents(connections)

view if exists agents
view if exists audit
view if exists blobs
view if exists connections
view if exists env_vars
view if exists login
view if exists orgs
view if exists plugin_connections
view if exists plugins
view if exists proxymanager_state
view if exists review_groups
view if exists reviews
view if exists serviceaccounts
view if exists sessions
view if exists user_groups
view if exists users'