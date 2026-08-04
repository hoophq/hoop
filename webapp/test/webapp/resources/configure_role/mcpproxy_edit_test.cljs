(ns webapp.resources.configure-role.mcpproxy-edit-test
  "The MCP Gateway (mcpproxy) connection EDIT screen.

  Two things have to hold here, and neither did.

  The screen must render an MCP form at all. Routing sent this subtype to the
  generic httpproxy credentials form, which has no OAuth widget — so
  re-authorization could never produce a flow id on an edit, and the gateway's
  update path for adopting a login into a renewable grant
  (adoptMCPOAuthGrant) was unreachable code. A connection whose token was
  frozen at create time simply stopped working when the provider expired it.

  And the settings must survive a save. That generic form re-emits every env
  var as an HTTP header, so one save through it renamed MCP_TRANSPORT to
  HEADER_MCP_TRANSPORT — after which the agent refuses the connection for
  having no transport — and dropped the stdio command array.

  These run the real hydration, the real form and the real payload builder,
  because both failures live in the seams between them."
  (:require
   [clojure.string :as str]
   [cljs.test :refer-macros [deftest is use-fixtures]]
   [re-frame.core :as rf]
   [re-frame.db :as rf-db]
   [webapp.connections.views.setup.events.process-form :as edit-process-form]
   [webapp.resources.configure-role.credentials-tab :as credentials-tab]
   ;; Registers the subscriptions the form derefs and the events both flows
   ;; dispatch. Without these the form throws on a nil subscription and the
   ;; hydration silently does nothing.
   [webapp.resources.setup.events.subs]
   [webapp.resources.setup.events.effects]
   [webapp.resources.setup.events.mcp-catalog]
   [webapp.resources.setup.events.mcp-oauth]
   [webapp.connections.views.setup.events.subs]
   [webapp.connections.views.setup.events.effects]
   [webapp.connections.views.setup.events.db-events]
   [webapp.resources.configure-role.mcp-oauth-edit]))

(defonce ^:private queued (atom []))

(use-fixtures :each
  {:before (fn []
             (reset! queued [])
             (rf/reg-fx :dispatch (fn [event] (swap! queued conj event))))
   :after (fn [] (rf/clear-fx :dispatch))})

(defn- dispatch! [event]
  (rf/dispatch-sync event)
  (loop [guard 100]
    (when-let [queued-event (first @queued)]
      (when (zero? guard)
        (throw (ex-info "event queue did not settle" {:pending @queued})))
      (swap! queued subvec 1)
      (rf/dispatch-sync queued-event)
      (recur (dec guard)))))

(defn- b64 [s]
  (js/btoa s))

(defn- saved-connection
  "A connection exactly as GET /connections/:name returns it: envvar: keys,
  base64 values, and the stdio command in the command array rather than the
  secret."
  [{:keys [envs command]}]
  {:name "figma-mcp"
   :type "httpproxy"
   :subtype "mcpproxy"
   :agent_id "agent-1"
   :command (vec command)
   :secret (into {} (map (fn [[k v]] [(keyword (str "envvar:" k)) (b64 v)]) envs))
   :redact_types []
   :reviewers []})

(def ^:private remote-connection
  (saved-connection
   {:envs {"MCP_TRANSPORT" "streamable-http"
           "REMOTE_URL" "https://mcp.linear.app/mcp"
           "MCP_AUTH" "static"
           "MCP_DENIED_TOOLS" "delete_*"
           "HEADER_AUTHORIZATION" "Bearer frozen-token"
           "INSECURE" "false"}}))

(def ^:private stdio-connection
  (saved-connection
   {:envs {"MCP_TRANSPORT" "stdio"
           "MCP_ON_RUG_PULL" "alert"
           "MCPENV_FIGMA_TOKEN" "sk-1"}
    :command ["npx" "-y" "figma-mcp"]}))

(defn- open!
  "Everything the edit screen does on mount, minus React: hydrate the saved
  connection into the create-flow role the form reads."
  [connection]
  (reset! rf-db/app-db {})
  (dispatch! [:connection-setup/initialize-state
              (edit-process-form/process-connection-for-update connection [] [])])
  (dispatch! [:resource-setup->set-mcpproxy-edit-role
              0 (edit-process-form/mcpproxy-edit-role connection)])
  (rf/clear-subscription-cache!))

(defn- rendered
  "Every string the credentials tab renders for this connection.

  A child component named in the hiccup is both expanded and read as written:
  expanding it is the routing assertion (reaching the MCP form's text at all
  means the tab chose that form), while its props are where a label like
  \"Command\" lives — forms/input is a form-2 component, so expanding it
  yields a closure rather than the label it was handed."
  [connection]
  (letfn [(walk [form]
            (cond
              (string? form) [form]
              (map? form) (mapcat walk (vals form))
              (and (vector? form) (fn? (first form)))
              (concat (mapcat walk (rest form))
                      (walk (apply (first form) (rest form))))
              (sequential? form) (mapcat walk form)
              :else []))]
    (vec (walk (credentials-tab/main connection)))))

(defn- shows? [texts needle]
  (boolean (some #(str/includes? % needle) texts)))

(defn- payload []
  (edit-process-form/process-payload @rf-db/app-db))

(defn- envs
  "Connection env vars the update would send, decoded back to plain strings."
  []
  (->> (js->clj (:secret (payload)))
       (map (fn [[k v]] [(subs k (count "envvar:")) (js/atob v)]))
       (into {})))

;; ---------------------------------------------------------------------------
;; The screen renders an MCP form
;; ---------------------------------------------------------------------------

;; Routing, pinned. Before the fix mcpproxy fell through to the generic
;; httpproxy branch, whose form renders no MCP anything.
(deftest the-edit-screen-renders-the-mcp-gateway-form
  (open! remote-connection)
  (let [texts (rendered remote-connection)]
    (is (shows? texts "MCP Server"))
    (is (shows? texts "MCP Authorization"))
    (is (shows? texts "Tool policy"))))

;; The OAuth widget is the point of the whole edit screen for this subtype:
;; without it a frozen token can never be replaced.
;;
;; A saved connection reopens in the static mode, because MCP_AUTH_MODE is
;; deliberately never emitted (it is form bookkeeping, not a setting the agent
;; reads) and MCP_AUTH collapses oauth to static — hoop brokered the login and
;; the agent sees a credential indistinguishable from a pasted one. So the
;; admin picks the OAuth mode again, and what must appear then is the widget
;; that produces a flow id.
(deftest the-edit-screen-offers-re-authorization
  (open! remote-connection)
  (dispatch! [:mcp-catalog/select-auth-mode 0 "oauth"])
  (is (shows? (rendered remote-connection) "Authorize with MCP")))

;; A stdio connection edits its command, not a URL. The command lives in the
;; connection's command array, so hydration has to look there — reading only
;; the env vars would show an empty field and save an empty command.
(deftest a-stdio-connection-opens-on-its-command
  (open! stdio-connection)
  (let [texts (rendered stdio-connection)]
    (is (shows? texts "Command"))
    (is (not (shows? texts "MCP Authorization"))))
  (is (= "npx -y figma-mcp"
         (get-in @rf-db/app-db [:resource-setup :roles 0 :credentials "command"]))))

;; ---------------------------------------------------------------------------
;; The flow id reaches the update payload
;; ---------------------------------------------------------------------------

;; The half of the OAuth change that could not execute. Re-authorizing on an
;; edit produces a flow id, and only that id lets the gateway adopt the login
;; into a grant it can renew; without it the connection keeps a header that
;; dies at the provider's TTL.
(deftest re-authorizing-on-an-edit-sends-the-flow-id
  (open! remote-connection)
  (dispatch! [:mcp-oauth/fetch-token-success
              0
              {:authorization_header "Bearer fresh-token"
               :server_url "https://mcp.linear.app/mcp"}
              "flow-abc"])
  (let [body (payload)]
    (is (= "flow-abc" (:mcp_oauth_flow_id body)))
    (is (= "Bearer fresh-token" (get (envs) "HEADER_AUTHORIZATION")))))

;; No re-authorization, no flow id. Sending a stale one would re-adopt a login
;; the admin did not perform on this save.
(deftest saving-without-re-authorizing-sends-no-flow-id
  (open! remote-connection)
  (is (not (contains? (payload) :mcp_oauth_flow_id))))

;; ---------------------------------------------------------------------------
;; The settings survive the save
;; ---------------------------------------------------------------------------

;; What the generic form did to this connection type. MCP_TRANSPORT is a
;; setting the agent parses; re-emitted as HEADER_MCP_TRANSPORT it becomes an
;; outbound HTTP header, and validateMCPProxyEnv then refuses the connection
;; for having no transport at all.
(deftest a-round-trip-does-not-rename-the-mcp-settings
  (open! remote-connection)
  (let [e (envs)]
    (is (= "streamable-http" (get e "MCP_TRANSPORT")))
    (is (= "static" (get e "MCP_AUTH")))
    (is (= "delete_*" (get e "MCP_DENIED_TOOLS")))
    (is (= "https://mcp.linear.app/mcp" (get e "REMOTE_URL")))
    (doseq [k ["HEADER_MCP_TRANSPORT" "HEADER_MCP_AUTH" "HEADER_MCP_DENIED_TOOLS"]]
      (is (not (contains? e k)) (str k " is a setting rewritten into a header")))))

;; A stdio connection round-trips its command through the command ARRAY. The
;; generic form has no notion of one, so saving through it left the agent with
;; nothing to spawn.
(deftest a-stdio-round-trip-keeps-its-command-array
  (open! stdio-connection)
  (let [body (payload)]
    (is (= ["npx" "-y" "figma-mcp"] (:command body)))
    (is (not (contains? (envs) "COMMAND"))))
  ;; And its child's secrets stay on the MCPENV_ side, where a subprocess can
  ;; read them. HEADER_ would put them on the wire of a connection that makes
  ;; no HTTP requests.
  (let [e (envs)]
    (is (= "sk-1" (get e "MCPENV_FIGMA_TOKEN")))
    (is (not (contains? e "HEADER_FIGMA_TOKEN")))
    (is (not (contains? e "HEADER_MCPENV_FIGMA_TOKEN")))))

;; Editing one field must not disturb the rest. This is the regression the
;; round-trip tests above would miss: they never write anything.
(deftest editing-the-tool-policy-leaves-the-transport-alone
  (open! remote-connection)
  (dispatch! [:resource-setup->update-role-credentials 0 "mcp_denied_tools" "delete_*, admin_*"])
  (let [e (envs)]
    (is (= "delete_*, admin_*" (get e "MCP_DENIED_TOOLS")))
    (is (= "streamable-http" (get e "MCP_TRANSPORT")))
    (is (= "Bearer frozen-token" (get e "HEADER_AUTHORIZATION")))))

;; The form's own bookkeeping must not become connection settings on an edit
;; either. mcp_static_header in particular would leak a header name as a
;; settings key.
(deftest the-edit-payload-carries-no-form-bookkeeping
  (open! remote-connection)
  (let [e (envs)]
    (doseq [k ["MCP_SERVER" "MCP_AUTH_MODE" "MCP_STATIC_HEADER"]]
      (is (not (contains? e k)) (str k " must not be emitted")))))

;; The subtype and the connection's identity are not the form's to change.
(deftest the-edit-payload-keeps-the-connection-identity
  (open! remote-connection)
  (let [body (payload)]
    (is (= "mcpproxy" (:subtype body)))
    (is (= "httpproxy" (:type body)))
    (is (= "figma-mcp" (:name body)))))

;; Roles hydrated for the edit screen are scratch state. Left in app-db the
;; next create wizard would open on this connection's credentials.
(deftest closing-the-edit-screen-drops-the-hydrated-role
  (open! remote-connection)
  (dispatch! [:resource-setup->clear-mcpproxy-edit-role 0])
  (is (nil? (get-in @rf-db/app-db [:resource-setup :roles 0]))))
