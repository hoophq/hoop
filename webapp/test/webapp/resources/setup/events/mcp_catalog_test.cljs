(ns webapp.resources.setup.events.mcp-catalog-test
  "Auth-mode selection for the MCP Gateway connection form.

  A catalog entry documents one auth mode, but several providers accept two:
  github, linear and stripe all take either an OAuth login or a personal
  access token. The mode is therefore the admin's choice, seeded from the
  catalog. What must hold across a change of mind:

    - the mode the admin picks is what the form renders and what MCP_AUTH says
    - a credential collected under the previous mode is gone, not merely hidden
    - a pasted token rides in the header its provider named, byte for byte

  These run the real events against app-db, because the bugs they defend
  against are all about what survives a dispatch."
  (:require
   [cljs.test :refer-macros [deftest is use-fixtures]]
   [re-frame.core :as rf]
   [re-frame.db :as rf-db]
   [webapp.resources.setup.events.mcp-catalog :as mcp-catalog]
   [webapp.resources.setup.events.process-form :as process-form]
   ;; Registers :resource-setup->update-role-credentials and
   ;; :resource-setup->remove-role-credential, which the events under test
   ;; dispatch. Without it every assertion fails on an unregistered handler.
   [webapp.resources.setup.events.effects]
   ;; Registers :mcp-oauth/clear, dispatched when the mode changes.
   [webapp.resources.setup.events.mcp-oauth]))

;; The events under test do their real work through :fx dispatches, which
;; re-frame's router queues for a later tick — so an assertion taken right
;; after dispatch-sync reads app-db mid-flight.
;;
;; re-frame exposes no queue flush, and dispatch-sync refuses to nest inside a
;; handler, so the :dispatch effect collects events here and `settle!` runs
;; them FIFO afterwards. Same order the router would use, minus the tick.
;; Async tests would hide an ordering bug behind a timeout instead.
(defonce ^:private queued (atom []))

(use-fixtures :each
  {:before (fn []
             (reset! queued [])
             (rf/reg-fx :dispatch (fn [event] (swap! queued conj event))))
   :after (fn [] (rf/clear-fx :dispatch))})

(defn- settle!
  "Run every event queued by the effects of the dispatch just made, and
  everything those in turn queue, until nothing is left."
  []
  (loop [guard 100]
    (when-let [event (first @queued)]
      (when (zero? guard)
        (throw (ex-info "event queue did not settle" {:pending @queued})))
      (swap! queued subvec 1)
      (rf/dispatch-sync event)
      (recur (dec guard)))))

;; A slice of the real catalog: one oauth server whose notes advertise a PAT,
;; one static server whose credential is NOT a bearer token, and one open
;; server. These three shapes are the whole decision space.
(def ^:private entries
  [{:name "github"
    :description "GitHub repositories, issues, PRs (official)"
    :url "https://api.githubcopilot.com/mcp"
    :transport "streamable-http"
    :auth "static"
    :header "Authorization: Bearer ${GITHUB_PAT}"
    :notes "OAuth also supported; a fine-grained PAT is the simplest setup"}
   {:name "linear"
    :url "https://mcp.linear.app/mcp"
    :transport "streamable-http"
    :auth "oauth"
    :notes "personal API keys also work as a static bearer"}
   {:name "context7"
    :url "https://mcp.context7.com/mcp"
    :transport "streamable-http"
    :auth "static"
    :header "CONTEXT7_API_KEY: ${CONTEXT7_API_KEY}"}
   {:name "excalidraw"
    :url "https://mcp.excalidraw.com/mcp"
    :transport "streamable-http"
    :auth "none"}])

(defn- reset-db!
  "One mcpproxy role, manual-input, with the catalog already loaded."
  []
  (reset! rf-db/app-db
          {:mcp-catalog {:status :loaded :entries entries}
           :resource-setup {:roles [{:name "mcp-role"
                                     :type "httpproxy"
                                     :subtype "mcpproxy"
                                     :connection-method "manual-input"
                                     :credentials {}}]}})
  (rf/clear-subscription-cache!))

(defn- creds []
  (get-in @rf-db/app-db [:resource-setup :roles 0 :credentials]))

(defn- cred [k]
  (mcp-catalog/cred-value (get (creds) k)))

(defn- dispatch! [event]
  (rf/dispatch-sync event)
  (settle!))

(defn- pick! [server]
  (dispatch! [:mcp-catalog/select-server 0 server]))

(defn- mode! [m]
  (dispatch! [:mcp-catalog/select-auth-mode 0 m]))

(defn- set-cred! [k v]
  (dispatch! [:resource-setup->update-role-credentials 0 k v]))

(defn- emitted
  "Connection env vars the role would be saved with, decoded back to plain
  strings. This is the payload the agent parses, so it is the only place the
  form's intent can be checked end to end."
  []
  (let [role (get-in @rf-db/app-db [:resource-setup :roles 0])]
    (->> (js->clj (process-form/process-role-secret role))
         (map (fn [[k v]] [(subs k (count "envvar:")) (js/atob v)]))
         (into {}))))

;; ---------------------------------------------------------------------------
;; Seeding from the catalog
;; ---------------------------------------------------------------------------

(deftest picking-a-server-seeds-the-mode-its-provider-documents
  (reset-db!)
  (pick! "linear")
  (is (= "oauth" (cred "mcp_auth_mode")))

  (reset-db!)
  (pick! "context7")
  (is (= "static" (cred "mcp_auth_mode")))

  (reset-db!)
  (pick! "excalidraw")
  (is (= "none" (cred "mcp_auth_mode"))))

;; The agent accepts only none|static; oauth is brokered by hoop and arrives
;; frozen into a header. A mode that emitted MCP_AUTH=oauth would be rejected
;; at connection time, long after the admin saved it.
(deftest the-mode-maps-onto-an-auth-value-the-agent-accepts
  (is (= "static" (mcp-catalog/mcp-auth-env "oauth")))
  (is (= "static" (mcp-catalog/mcp-auth-env "static")))
  (is (= "none" (mcp-catalog/mcp-auth-env "none")))
  (reset-db!)
  (pick! "linear")
  (is (= "static" (cred "mcp_auth")))
  (mode! "none")
  (is (= "none" (cred "mcp_auth"))))

;; A server the catalog does not know still needs a working static field, and
;; the bearer convention is the only sane default.
(deftest a-custom-server-defaults-to-a-bearer-header
  (reset-db!)
  (pick! "custom")
  (is (= "custom" (cred "mcp_server")))
  (is (= "Authorization" (cred "mcp_static_header")))
  (is (= "HEADER_Authorization" (mcp-catalog/static-token-key (creds)))))

;; A half-typed URL must survive picking "custom", which is the escape hatch
;; for exactly the server whose URL the admin is typing.
(deftest picking-custom-keeps-what-the-admin-typed
  (reset-db!)
  (set-cred! "remote_url" "https://mcp.internal/mcp")
  (pick! "custom")
  (is (= "https://mcp.internal/mcp" (cred "remote_url"))))

;; ---------------------------------------------------------------------------
;; Overriding the mode
;; ---------------------------------------------------------------------------

;; The reason this feature exists: github's catalog entry says "static", its
;; notes say OAuth also works. An admin who wants OAuth must be able to get it.
(deftest a-static-server-can-be-switched-to-oauth
  (reset-db!)
  (pick! "github")
  (is (= "static" (cred "mcp_auth_mode")))
  (mode! "oauth")
  (is (= "oauth" (cred "mcp_auth_mode")))
  (is (= "static" (cred "mcp_auth"))))

;; And the reverse: linear is an oauth entry whose notes offer a personal API
;; key. Picking the static mode must produce a field that reaches the wire.
(deftest an-oauth-server-can-be-switched-to-a-token
  (reset-db!)
  (pick! "linear")
  (mode! "static")
  (set-cred! (mcp-catalog/static-token-key (creds)) "lin_api_key")
  (let [envs (emitted)]
    (is (= "static" (get envs "MCP_AUTH")))
    (is (= "lin_api_key" (get envs "HEADER_Authorization")))))

;; ---------------------------------------------------------------------------
;; Forgetting the previous mode's credential
;; ---------------------------------------------------------------------------

;; A token left behind by the previous mode would keep authenticating while
;; the form shows a mode that never collected it — the failure is silent, and
;; it is a credential.
(deftest switching-away-from-static-forgets-the-pasted-token
  (reset-db!)
  (pick! "context7")
  (set-cred! "HEADER_CONTEXT7_API_KEY" "ctx-secret")
  (is (= "ctx-secret" (cred "HEADER_CONTEXT7_API_KEY")))
  (mode! "oauth")
  (is (not (contains? (creds) "HEADER_CONTEXT7_API_KEY")))
  (is (not (contains? (emitted) "HEADER_CONTEXT7_API_KEY"))))

(deftest switching-away-from-oauth-forgets-the-frozen-token
  (reset-db!)
  (pick! "linear")
  (set-cred! "HEADER_AUTHORIZATION" "Bearer frozen-oauth-token")
  (mode! "static")
  (is (not (contains? (creds) "HEADER_AUTHORIZATION")))
  (is (not (contains? (emitted) "HEADER_AUTHORIZATION"))))

;; The two spellings the form can produce — HEADER_AUTHORIZATION from the OAuth
;; flow, HEADER_Authorization from the default static header — are one header
;; upstream. Dropping only the exact key would leave the other behind and send
;; a stale credential.
(deftest forgetting-ignores-header-name-casing
  (reset-db!)
  (pick! "custom")
  (set-cred! "HEADER_AUTHORIZATION" "Bearer from-oauth")
  (set-cred! "HEADER_Authorization" "pat-from-form")
  (mode! "none")
  (is (empty? (filter #(= "header_authorization" (.toLowerCase %)) (keys (creds))))))

(deftest switching-to-none-emits-no-credential
  (reset-db!)
  (pick! "github")
  (set-cred! "HEADER_Authorization" "ghp_token")
  (mode! "none")
  (let [envs (emitted)]
    (is (= "none" (get envs "MCP_AUTH")))
    (is (not (contains? envs "HEADER_Authorization")))))

;; ---------------------------------------------------------------------------
;; What reaches the agent
;; ---------------------------------------------------------------------------

;; The provider names the header, and it is often not Authorization. Sending
;; the token under a rewritten name is an unauthenticated request, not a
;; warning, so the name must survive the emission step byte for byte.
(deftest a-provider-named-header-reaches-the-wire-verbatim
  (reset-db!)
  (pick! "context7")
  (is (= "HEADER_CONTEXT7_API_KEY" (mcp-catalog/static-token-key (creds))))
  (set-cred! "HEADER_CONTEXT7_API_KEY" "ctx-secret-value")
  (let [envs (emitted)]
    (is (= "ctx-secret-value" (get envs "HEADER_CONTEXT7_API_KEY")))
    (is (= "https://mcp.context7.com/mcp" (get envs "REMOTE_URL")))
    (is (= "streamable-http" (get envs "MCP_TRANSPORT")))))

;; The form's own bookkeeping is not connection configuration. Emitting it
;; would create env vars the agent never reads, and mcp_static_header in
;; particular would leak a header name as a settings key.
(deftest form-bookkeeping-never-becomes-a-connection-setting
  (reset-db!)
  (pick! "github")
  (mode! "oauth")
  (let [envs (emitted)]
    (doseq [k ["MCP_SERVER" "MCP_AUTH_MODE" "MCP_STATIC_HEADER"]]
      (is (not (contains? envs k)) (str k " must not be emitted")))
    ;; The mode still reaches the agent, under the name it does read.
    (is (= "static" (get envs "MCP_AUTH")))))
