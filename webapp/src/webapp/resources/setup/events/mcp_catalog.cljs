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
   [re-frame.core :as rf]))

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

(rf/reg-event-db
 :mcp-catalog/fetch-success
 (fn [db [_ response]]
   (assoc db :mcp-catalog {:status :loaded
                           :entries (js->clj response :keywordize-keys true)})))

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
;; A catalog entry records ONE auth mode, but that is the provider's default,
;; not its only option. GitHub, Linear and Stripe all accept either an OAuth
;; login or a personal access token, and their catalog notes say so in prose
;; the form used to ignore: the widget was chosen by (:auth entry) alone, so
;; an admin holding a PAT for an oauth-mode server had no field to paste it
;; into, and vice versa.
;;
;; So the mode is credential state the admin owns ("mcp_auth_mode"), seeded
;; from the catalog and overridable. It is UI-only — process-role-secret drops
;; it. What reaches the agent is MCP_AUTH plus whichever HEADER_* the chosen
;; mode produced.

(def auth-modes
  "Selectable auth modes, in the order the form offers them."
  [{:value "oauth" :text "OAuth login (Hoop brokers the flow)"}
   {:value "static" :text "API key or personal access token"}
   {:value "none" :text "No authentication"}])

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

(defn mcp-auth-env
  "MCP_AUTH the agent receives for a chosen mode.

  The agent accepts only none|static (agent/controller/mcpproxy.go): an OAuth
  login is brokered by hoop and frozen into a header, so by the time the agent
  sees the connection it is indistinguishable from a static credential."
  [mode]
  (if (= mode "none") "none" "static"))

(defn cred-value
  "Plain string held by a role credential. A credential is either the raw value
  or the {:value :source} shape the secrets-manager method wraps it in, and a
  header NAME read out of the wrapped shape would build a garbage env var key."
  [v]
  (cond
    (map? v) (str (:value v ""))
    (nil? v) ""
    :else (str v)))

(defn static-token-key
  "Credential key holding a pasted token for these credentials. The header name
  travels inside the key so config->json emits it verbatim; upper-casing or
  hyphenating it would authenticate as nobody."
  [credentials]
  (let [h (cred-value (get credentials "mcp_static_header"))]
    (str "HEADER_" (if (str/blank? h) default-static-header h))))

;; ---------------------------------------------------------------------------
;; Applying a selection
;; ---------------------------------------------------------------------------

(rf/reg-event-fx
 :mcp-catalog/select-server
 (fn [{:keys [db]} [_ role-index server-name]]
   (let [entries (get-in db [:mcp-catalog :entries] [])
         entry (entry-by-name entries server-name)
         set-cred (fn [k v] [:dispatch [:resource-setup->update-role-credentials role-index k v]])]
     (if (nil? entry)
       ;; "custom": remember the choice and leave the fields as the admin left
       ;; them. Clearing them here would discard a half-typed URL.
       {:fx [(set-cred "mcp_server" "custom")
             (set-cred "mcp_static_header" default-static-header)]}
       (let [mode (default-auth-mode entry)]
         {:fx [(set-cred "mcp_server" server-name)
               (set-cred "remote_url" (:url entry))
               (set-cred "mcp_transport" (:transport entry))
               (set-cred "mcp_auth_mode" mode)
               (set-cred "mcp_auth" (mcp-auth-env mode))
               ;; Record which header the provider expects so the form can ask
               ;; for the token by name instead of making the admin decode a
               ;; "${CONTEXT7_API_KEY}" template.
               (set-cred "mcp_static_header" (static-header-for entry))]})))))

;; Switching mode forgets every credential the auth modes own, without regard
;; for which mode wrote it. Forgetting is the point: a leftover token would
;; silently authenticate under a mode the admin believes they replaced. It
;; cannot be narrowed by key either — a frozen OAuth token and a bearer PAT
;; both live in the Authorization header, and the two spellings the form can
;; produce ("HEADER_AUTHORIZATION" from the OAuth flow, "HEADER_Authorization"
;; from the default static header) canonicalize to one header upstream.
(rf/reg-event-fx
 :mcp-catalog/select-auth-mode
 (fn [{:keys [db]} [_ role-index mode]]
   (let [creds (get-in db [:resource-setup :roles role-index :credentials] {})
         owned? (let [owned (set (map str/lower-case
                                      [(static-token-key creds) "HEADER_AUTHORIZATION"]))]
                  (comp owned str/lower-case))]
     {:fx (into [[:dispatch [:resource-setup->update-role-credentials role-index "mcp_auth_mode" mode]]
                 [:dispatch [:resource-setup->update-role-credentials role-index "mcp_auth" (mcp-auth-env mode)]]
                 ;; Resets the OAuth widget too: left on :success it would
                 ;; render "Authorized" under a mode holding no token.
                 [:dispatch [:mcp-oauth/clear role-index]]]
                (comp (filter owned?)
                      (map (fn [k] [:dispatch [:resource-setup->remove-role-credential role-index k]])))
                (keys creds))})))
