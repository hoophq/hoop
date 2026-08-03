(ns webapp.resources.setup.events.mcp-catalog
  "Server picker for the MCP Gateway (mcpproxy) connection type.

  The gateway serves mcpproxy's built-in catalog of publicly hosted remote MCP
  servers at GET /mcp-catalog. Picking an entry pre-fills the endpoint,
  transport and auth mode so an admin does not have to look those values up;
  the \"custom\" choice leaves every field editable for a server the catalog
  does not know, including local stdio servers.

  The catalog is static build-time data on the gateway side, so it is fetched
  once per app session and cached in app-db under [:mcp-catalog]:

    {:status  :idle | :loading | :loaded | :error
     :entries [{:name :description :url :transport :auth :header :notes}]}"
  (:require
   [clojure.string :as str]
   [re-frame.core :as rf]
   [webapp.resources.constants :refer [mcp-stdio-transports]]
   [webapp.resources.setup.events.process-form :refer [raw-credential-value]]))

;; ---------------------------------------------------------------------------
;; Fetch + cache
;; ---------------------------------------------------------------------------

(rf/reg-event-fx
 :mcp-catalog/fetch
 (fn [{:keys [db]} _]
   ;; Already loaded or in flight: the catalog cannot change within a session.
   (if (contains? #{:loading :loaded} (get-in db [:mcp-catalog :status]))
     {}
     {:db (assoc-in db [:mcp-catalog :status] :loading)
      :fx [[:dispatch [:fetch {:method "GET"
                               :uri "/mcp-catalog"
                               :on-success #(rf/dispatch [:mcp-catalog/fetch-success %])
                               :on-failure #(rf/dispatch [:mcp-catalog/fetch-failure %])}]]]})))

;; JSON keys arrive exactly as the gateway spells them, and :keywordize-keys
;; does NOT translate underscores to dashes: "auth_modes" becomes :auth_modes,
;; never :auth-modes. Reading the kebab-case name downstream silently yields
;; nil, which for the auth modes meant every server looked single-mode and the
;; picker stopped offering a choice. Normalize once, here, so the rest of the
;; namespace can use one spelling.
(defn normalize-entry
  "One catalog entry in the shape the rest of this namespace expects."
  [entry]
  (-> entry
      (assoc :auth-modes (vec (or (:auth_modes entry) (:auth-modes entry))))
      (dissoc :auth_modes)))

(rf/reg-event-db
 :mcp-catalog/fetch-success
 (fn [db [_ response]]
   (assoc db :mcp-catalog
          {:status :loaded
           :entries (mapv normalize-entry
                          (js->clj response :keywordize-keys true))})))

(rf/reg-event-db
 :mcp-catalog/fetch-failure
 (fn [db [_ _error]]
   ;; A failed catalog fetch must not block the form: the admin can still fill
   ;; the fields by hand, which is exactly the "custom" path.
   (assoc db :mcp-catalog {:status :error :entries []})))

(rf/reg-sub
 :mcp-catalog/state
 (fn [db _]
   (get db :mcp-catalog {:status :idle :entries []})))

;; ---------------------------------------------------------------------------
;; Catalog entries
;; ---------------------------------------------------------------------------

(defn- entry-by-name
  [entries server-name]
  (first (filter #(= (:name %) server-name) entries)))

;; ---------------------------------------------------------------------------
;; Auth mode
;; ---------------------------------------------------------------------------
;;
;; A catalog entry records ONE default auth mode, but for a few providers that
;; is only a default: github, linear and stripe each accept either an OAuth
;; login or a long-lived token. The form used to pick its widget from
;; (:auth entry) alone, so an admin holding a PAT for an oauth-mode server had
;; no field to paste it into, and vice versa.
;;
;; The other 27 servers accept exactly one mode, and offering more is its own
;; bug: an OAuth login against google-maps runs RFC 9728 discovery on an
;; endpoint that publishes no authorization server, and the admin only learns
;; that after clicking through. So the gateway sends :auth-modes per entry
;; (see gateway/api/connections/connection_mcp_catalog.go) and the form offers
;; exactly those.
;;
;; The chosen mode is credential state the admin owns ("mcp_auth_mode"). It is
;; UI-only — process-role-secret drops it. What reaches the agent is MCP_AUTH
;; plus whichever HEADER_* the chosen mode produced.

(def auth-mode-labels
  "How each mode is described, in the order the form offers them."
  [{:value "oauth" :text "OAuth login (Hoop brokers the flow)"}
   {:value "static" :text "API key or personal access token"}
   {:value "passthrough" :text "Each user sends their own credential"}
   {:value "none" :text "No authentication"}])

(def ^:private all-auth-modes
  (mapv :value auth-mode-labels))

(defn auth-modes
  "Modes to offer for a catalog entry, as {:value :text} options.

  A server the catalog does not know (custom/self-hosted) gets every mode:
  hoop cannot know what it accepts, and the admin does. A known server gets
  what the gateway said it supports — anything else is a flow the provider
  cannot complete.

  Passthrough is the exception, and it is a hoop capability rather than a
  provider one: it sends a bearer credential in exactly the same header a
  static token uses, only sourced from each caller instead of the connection.
  So any server that documents a static credential can serve it, and the
  catalog never needs to list it. A server that takes no credential at all
  (auth: none) still cannot.

  An older gateway that predates :auth-modes sends nothing; fall back to the
  entry's single documented mode rather than rendering an empty selector."
  [entry]
  (let [supported (cond
                    (nil? entry) all-auth-modes
                    (seq (:auth-modes entry)) (:auth-modes entry)
                    (not (str/blank? (:auth entry))) [(:auth entry)]
                    :else all-auth-modes)
        supported (set supported)
        supported (if (contains? supported "static")
                    (conj supported "passthrough")
                    supported)]
    (filterv (comp supported :value) auth-mode-labels)))

(defn static-header-name
  "Header name a catalog entry expects for a pasted credential, from its
  \"Name: ${TEMPLATE}\" header field. nil when the entry names none.

  The name matters exactly: context7 wants CONTEXT7_API_KEY and google-maps
  wants X-Goog-Api-Key. Sending the token under the wrong header name is an
  unauthenticated request, not a warning.

  Read regardless of the entry's own auth mode: an oauth-mode entry that also
  documents a static header (none do today, but the schema allows it) must
  still use that name when the admin picks the static mode."
  [entry]
  (let [header (or (:header entry) "")
        [name-part] (str/split header #":" 2)
        n (str/trim (or name-part ""))]
    (when-not (str/blank? n) n)))

(def default-static-header
  "Header a pasted credential rides in when nothing names another one. Every
  catalog server that documents a static credential without naming a header,
  and every self-hosted server, uses the bearer convention."
  "Authorization")

(defn static-header-for
  "The header a pasted credential must use for this entry."
  [entry]
  (or (static-header-name entry) default-static-header))

(defn default-auth-mode
  "Auth mode a freshly picked entry starts in: the one its provider documents.
  An unknown (custom/self-hosted) server starts on the static mode, which is
  the only one that needs no discovery endpoint to work."
  [entry]
  (case (:auth entry)
    "oauth" "oauth"
    "none" "none"
    "static"))

(defn coerce-auth-mode
  "The mode to render, given what the admin last chose and what this server
  accepts. A stored mode the server does not support falls back to its
  default.

  This is what makes changing the server picker safe: pick github, choose
  OAuth, then switch to google-maps, and \"oauth\" is still sitting in the
  credentials. Rendering it would offer a login google-maps cannot serve."
  [entry mode]
  (let [supported (set (map :value (auth-modes entry)))]
    (if (contains? supported mode)
      mode
      (default-auth-mode entry))))

(defn mcp-auth-env
  "MCP_AUTH the agent receives for a chosen mode.

  The agent accepts none|static|passthrough (agent/controller/mcpproxy.go).
  OAuth is not among them and does not need to be: hoop brokers that login
  itself and resolves the result into HEADER_AUTHORIZATION before the session
  opens, so the agent sees a credential indistinguishable from a static one.

  Passthrough is different in kind and must travel verbatim: there is no
  credential on the connection for the agent to send, and it has to know to
  take one off each inbound request instead. Collapsing it to \"static\" would
  produce a backend that authenticates as nobody."
  [mode]
  (case mode
    "none" "none"
    "passthrough" "passthrough"
    "static"))

(defn static-token-key
  "Credential key holding a pasted token for these credentials. The header name
  travels inside the key so config->json emits it verbatim; upper-casing or
  hyphenating it would authenticate as nobody.

  Unwrapped before use: a header NAME read out of the {:value :source} shape
  the secrets-manager method wraps credentials in would build a garbage env
  var key."
  [credentials]
  (let [h (raw-credential-value (get credentials "mcp_static_header"))]
    (str "HEADER_" (if (str/blank? h) default-static-header h))))

;; ---------------------------------------------------------------------------
;; Applying a selection
;; ---------------------------------------------------------------------------

(defn auth-credential-keys
  "Credential keys holding a credential the auth widget collected.

  Every HEADER_* in a role's :credentials came from that widget: extra headers
  an admin adds live in :environment-variables and only gain the HEADER_
  prefix at emission (process-role-secret). So this is safe to clear wholesale
  when the server or mode changes, and clearing wholesale is what makes the
  two spellings the widget can produce — HEADER_AUTHORIZATION from the OAuth
  flow, HEADER_Authorization from the default static header — both go."
  [credentials]
  (filter #(str/starts-with? (str/lower-case %) "header_") (keys credentials)))

;; ---------------------------------------------------------------------------
;; Changing the transport
;; ---------------------------------------------------------------------------
;;
;; The transport decides what every other field on the form MEANS, so changing
;; it has to coerce the credentials the previous transport owned. Nothing else
;; does: the dropdown used to write mcp_transport and stop there, and the
;; leftovers are all emitted.
;;
;; What that cost, concretely. An admin configures a remote server with
;; MCP_AUTH=passthrough, then switches the transport to stdio. The agent
;; rejects that pair outright (validateMCPProxyEnv: passthrough substitutes a
;; caller's HTTP credential, and a subprocess has no inbound request to take
;; one off), and the MCP Authorization block that would let them change it is
;; hidden the moment stdio is selected. The connection cannot be saved into a
;; working state from the form that produced it. The reverse direction is
;; quieter but no better: a stale command rides out as an envvar:COMMAND that
;; nothing reads, next to the REMOTE_URL that replaced it.
;;
;; The invariant these functions keep: after a transport change the
;; credentials describe exactly ONE transport. A stdio role has a command and
;; MCPENV_* and no remote_url, no auth mode and no HTTP headers; a remote role
;; has a remote_url and an auth mode and no command.

(defn- stdio-transport? [transport]
  (contains? mcp-stdio-transports transport))

(defn transport-coercion-fx
  "Effects that make `credentials` describe `to` rather than `from`.

  Empty when both transports sit on the same side of the stdio/remote split:
  \"stdio\" to \"client-stdio\" changes which machine runs the command, not
  what any credential means, and re-picking a catalog server must not discard
  headers the admin typed for it.

  The role's :environment-variables go in BOTH directions because that one
  list is emitted under a different prefix on each side — MCPENV_* into a
  child process's environment for stdio, HEADER_* onto outbound HTTP requests
  for a remote server (process-role-secret). Carrying it across the switch
  either puts a subprocess secret on the wire or an HTTP header in a process
  environment, and neither is what the admin typed it for."
  [role-index credentials from to]
  (let [forget (fn [ks]
                 (map (fn [k] [:dispatch [:resource-setup->remove-role-credential role-index k]]) ks))]
    (cond
      (= (stdio-transport? from) (stdio-transport? to))
      []

      (stdio-transport? to)
      (into [;; The widget that collected the credential is about to
             ;; disappear, so its state has to go with it rather than sit
             ;; there being emitted.
             [:dispatch [:mcp-oauth/clear role-index]]
             [:dispatch [:resource-setup->set-role-env-vars role-index []]]]
            (forget (into ["mcp_auth" "mcp_auth_mode" "mcp_static_header" "remote_url"]
                          (auth-credential-keys credentials))))

      :else
      (into [[:dispatch [:resource-setup->set-role-env-vars role-index []]]]
            (forget ["command"])))))

(rf/reg-event-fx
 :mcp-catalog/select-transport
 (fn [{:keys [db]} [_ role-index transport]]
   (let [creds (get-in db [:resource-setup :roles role-index :credentials] {})
         from (raw-credential-value (get creds "mcp_transport"))]
     ;; Coercion first: it drops the credentials the old transport owned, and
     ;; the write below is what the form then renders from.
     {:fx (conj (vec (transport-coercion-fx role-index creds from transport))
                [:dispatch [:resource-setup->update-role-credentials
                            role-index "mcp_transport" transport]])})))

(rf/reg-event-fx
 :mcp-catalog/select-server
 (fn [{:keys [db]} [_ role-index server-name]]
   (let [entries (get-in db [:mcp-catalog :entries] [])
         entry (entry-by-name entries server-name)
         creds (get-in db [:resource-setup :roles role-index :credentials] {})
         set-cred (fn [k v] [:dispatch [:resource-setup->update-role-credentials role-index k v]])
         ;; A credential collected for the previous server authenticates to
         ;; nobody here, and left in place it would be saved with the new
         ;; connection.
         forget (into [[:dispatch [:mcp-oauth/clear role-index]]]
                      (map (fn [k] [:dispatch [:resource-setup->remove-role-credential role-index k]]))
                      (auth-credential-keys creds))
         mode (coerce-auth-mode entry (raw-credential-value (get creds "mcp_auth_mode")))
         from-transport (raw-credential-value (get creds "mcp_transport"))
         ;; The picker is the third way the transport changes, after the
         ;; dropdown and a fresh role's default: every catalog entry carries
         ;; its own. So a custom stdio server the admin typed a command for,
         ;; then replaced with a hosted one, needs the same coercion the
         ;; dropdown does — otherwise that command is still emitted, as a
         ;; COMMAND env var, next to the REMOTE_URL that superseded it.
         coerce (transport-coercion-fx role-index creds from-transport
                                       (or (:transport entry) from-transport))]
     (if (nil? entry)
       ;; "custom": remember the choice and leave the endpoint fields as the
       ;; admin left them. Clearing those would discard a half-typed URL.
       {:fx (into forget
                  [(set-cred "mcp_server" "custom")
                   (set-cred "mcp_static_header" default-static-header)
                   (set-cred "mcp_auth_mode" mode)
                   (set-cred "mcp_auth" (mcp-auth-env mode))])}
       {:fx (into (into (vec coerce) forget)
                  [(set-cred "mcp_server" server-name)
                   (set-cred "remote_url" (:url entry))
                   (set-cred "mcp_transport" (:transport entry))
                   (set-cred "mcp_auth_mode" mode)
                   (set-cred "mcp_auth" (mcp-auth-env mode))
                   ;; Record which header the provider expects so the form can
                   ;; ask for the token by name instead of making the admin
                   ;; decode a "${CONTEXT7_API_KEY}" template.
                   (set-cred "mcp_static_header" (static-header-for entry))])}))))

;; Switching mode forgets the credential the previous mode collected. A
;; leftover token would silently authenticate under a mode the admin believes
;; they replaced, and it cannot be narrowed by key: a frozen OAuth token and a
;; bearer PAT both live in the Authorization header.
(rf/reg-event-fx
 :mcp-catalog/select-auth-mode
 (fn [{:keys [db]} [_ role-index mode]]
   (let [creds (get-in db [:resource-setup :roles role-index :credentials] {})]
     {:fx (into [[:dispatch [:resource-setup->update-role-credentials role-index "mcp_auth_mode" mode]]
                 [:dispatch [:resource-setup->update-role-credentials role-index "mcp_auth" (mcp-auth-env mode)]]
                 ;; Resets the OAuth widget too: left on :success it would
                 ;; render "Authorized" under a mode holding no token.
                 [:dispatch [:mcp-oauth/clear role-index]]]
                (map (fn [k] [:dispatch [:resource-setup->remove-role-credential role-index k]]))
                (auth-credential-keys creds))})))
