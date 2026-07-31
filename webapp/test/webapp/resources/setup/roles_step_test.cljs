(ns webapp.resources.setup.roles-step-test
  "What the MCP Gateway form renders for each auth mode.

  The events tests next door prove the credential state is right; this proves
  the admin can see and reach it. Reagent components are plain functions of
  app-db, so the form is called directly and its hiccup inspected — no DOM.

  The bug this defends against: the form used to choose its auth widget from
  the catalog entry alone, so a github connection (catalog says static, notes
  say OAuth also works) rendered a token field and no way to reach the OAuth
  flow. Rendering the wrong widget makes a supported credential unreachable."
  (:require
   [clojure.string :as str]
   [cljs.test :refer-macros [deftest is use-fixtures]]
   [re-frame.core :as rf]
   [re-frame.db :as rf-db]
   [webapp.resources.setup.roles-step :as roles-step]
   ;; Registers the subscriptions the form derefs
   ;; (:resource-setup/role-credentials and friends) and the events it
   ;; dispatches. Without these the form throws on a nil subscription.
   [webapp.resources.setup.events.subs]
   [webapp.resources.setup.events.effects]
   [webapp.resources.setup.events.mcp-catalog :as mcp-catalog]
   [webapp.resources.setup.events.mcp-oauth]))

;; The literal JSON GET /mcp-catalog returns, snake_case keys and all, fed
;; through the real fetch-success handler below.
;;
;; Raw JSON on purpose: js->clj :keywordize-keys does NOT turn "auth_modes"
;; into :auth-modes, and hand-built kebab-case fixtures agreed with the code
;; while disagreeing with the gateway. The suite passed; github rendered a
;; token field and no OAuth option.
(def ^:private catalog-json
  (js/JSON.stringify
   (clj->js
    [{:name "github"
      :url "https://api.githubcopilot.com/mcp"
      :transport "streamable-http"
      :auth "static"
      :auth_modes ["static" "oauth"]
      :header "Authorization: Bearer ${GITHUB_PAT}"
      :notes "OAuth also supported; a fine-grained PAT is the simplest setup"}
     {:name "linear"
      :url "https://mcp.linear.app/mcp"
      :transport "streamable-http"
      :auth "oauth"
      :auth_modes ["oauth" "static"]
      :notes "personal API keys also work as a static bearer"}
     ;; Single-mode servers: context7 issues an API key under its own header
     ;; name, and notion publishes no static credential at all.
     {:name "context7"
      :url "https://mcp.context7.com/mcp"
      :transport "streamable-http"
      :auth "static"
      :auth_modes ["static"]
      :header "CONTEXT7_API_KEY: ${CONTEXT7_API_KEY}"}
     {:name "notion"
      :url "https://mcp.notion.com/mcp"
      :transport "streamable-http"
      :auth "oauth"
      :auth_modes ["oauth"]}
     {:name "excalidraw"
      :url "https://mcp.excalidraw.com/mcp"
      :transport "streamable-http"
      :auth "none"
      :auth_modes ["none"]}])))

;; :fx dispatches are queued by re-frame's router for a later tick, and
;; dispatch-sync cannot nest inside a handler. Collect them here and run them
;; FIFO, which is the order the router would have used.
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

(defn- reset-db! []
  (reset! rf-db/app-db
          {:resource-setup {:roles [{:name "mcp-role"
                                     :type "httpproxy"
                                     :subtype "mcpproxy"
                                     :connection-method "manual-input"
                                     :credentials {}}]}})
  ;; Through the real handler, so the form sees exactly the entry shape the
  ;; running app puts in app-db.
  (rf/dispatch-sync [:mcp-catalog/fetch-success (js/JSON.parse catalog-json)])
  (rf/clear-subscription-cache!))

(defn- rendered
  "Every string the form renders, flattened out of the hiccup tree. Labels,
  headings and button text all land here, which is what a reader of the page
  actually has to go on."
  []
  (letfn [(walk [form]
            (cond
              (string? form) [form]
              (map? form) (mapcat walk (vals form))
              (sequential? form) (mapcat walk form)
              :else []))]
    (vec (walk (roles-step/mcpproxy-role-form 0)))))

(defn- shows? [texts needle]
  (boolean (some #(str/includes? % needle) texts)))

(defn- pick! [server]
  (dispatch! [:mcp-catalog/select-server 0 server]))

(defn- mode! [m]
  (dispatch! [:mcp-catalog/select-auth-mode 0 m]))

;; A server that documents two credentials gets a real choice.
(deftest a-dual-mode-server-offers-both-of-its-modes
  (reset-db!)
  (pick! "github")
  (let [texts (rendered)]
    (is (shows? texts "Authentication method"))
    (is (some #{"API key or personal access token"} texts))
    (is (some #{"OAuth login (Hoop brokers the flow)"} texts))
    ;; github does not accept anonymous access, so that option is absent.
    (is (not (some #{"No authentication"} texts)))))

;; A dropdown holding one option is a decision the admin does not have, and
;; offering a mode the provider cannot serve is worse than not offering it:
;; an OAuth login against context7 discovers no authorization server, and the
;; dead end only shows up after the admin clicks.
(deftest a-single-mode-server-offers-no-choice
  (reset-db!)
  (pick! "context7")
  (let [texts (rendered)]
    (is (not (shows? texts "Authentication method")))
    (is (not (some #{"OAuth login (Hoop brokers the flow)"} texts)))
    ;; Still says how it authenticates, and still asks for the credential.
    (is (some #{"API key or personal access token"} texts))
    (is (shows? texts "CONTEXT7_API_KEY value"))))

(deftest an-oauth-only-server-offers-no-token-field
  (reset-db!)
  (pick! "notion")
  (let [texts (rendered)]
    (is (not (shows? texts "Authentication method")))
    (is (shows? texts "Authorize with MCP"))
    (is (not (shows? texts "Authorization value")))))

;; A server the catalog does not know: hoop cannot know what it accepts, so
;; the admin gets every mode.
(deftest a-custom-server-offers-every-mode
  (reset-db!)
  (pick! "custom")
  (let [texts (rendered)]
    (is (shows? texts "Authentication method"))
    (doseq [option ["OAuth login (Hoop brokers the flow)"
                    "API key or personal access token"
                    "No authentication"]]
      (is (some #{option} texts) (str "missing option: " option)))))

;; The regression in one test: github seeds static, and switching to OAuth has
;; to actually swap the widget. Before, there was no way to get here.
(deftest a-static-server-can-be-switched-to-the-oauth-widget
  (reset-db!)
  (pick! "github")
  (is (shows? (rendered) "Authorization value"))
  (mode! "oauth")
  (let [texts (rendered)]
    (is (shows? texts "Authorize with MCP"))
    (is (not (shows? texts "Authorization value")))))

;; And the reverse: linear is an oauth entry that also accepts an API key.
(deftest an-oauth-server-can-be-switched-to-a-token-field
  (reset-db!)
  (pick! "linear")
  (is (shows? (rendered) "Authorize with MCP"))
  (mode! "static")
  (let [texts (rendered)]
    (is (shows? texts "Authorization value"))
    (is (not (shows? texts "Authorize with MCP")))))

;; Changing the server must not leave the previous server's mode on screen.
;; github -> OAuth -> context7 would otherwise render an OAuth button for a
;; server that only takes an API key.
(deftest changing-server-drops-a-mode-the-new-one-cannot-serve
  (reset-db!)
  (pick! "github")
  (mode! "oauth")
  (is (shows? (rendered) "Authorize with MCP"))
  (pick! "context7")
  (let [texts (rendered)]
    (is (not (shows? texts "Authorize with MCP")))
    (is (shows? texts "CONTEXT7_API_KEY value"))
    (is (= "static" (mcp-catalog/cred-value
                     (get-in @rf-db/app-db
                             [:resource-setup :roles 0 :credentials "mcp_auth_mode"]))))))

;; The field must name the header the provider expects. An admin who pastes a
;; context7 key into a box labelled "Authorization" has been told the wrong
;; thing about where it goes.
(deftest the-token-field-names-the-header-the-provider-expects
  (reset-db!)
  (pick! "context7")
  (let [texts (rendered)]
    (is (shows? texts "CONTEXT7_API_KEY value"))
    (is (shows? texts "Sent to the server in the CONTEXT7_API_KEY header"))))

(deftest the-none-mode-asks-for-nothing
  (reset-db!)
  (pick! "excalidraw")
  (let [texts (rendered)]
    (is (shows? texts "No credential is sent"))
    (is (not (shows? texts "Authorization value")))
    (is (not (shows? texts "Authorize with MCP")))))

;; A stdio server authenticates through its child environment (MCPENV_*), so
;; the whole block is meaningless there and must not appear.
(deftest stdio-transports-render-no-auth-block
  (reset-db!)
  (pick! "github")
  (dispatch! [:resource-setup->update-role-credentials 0 "mcp_transport" "stdio"])
  (let [texts (rendered)]
    (is (not (shows? texts "MCP Authorization")))
    (is (not (shows? texts "Authentication method")))
    (is (shows? texts "Command"))))
