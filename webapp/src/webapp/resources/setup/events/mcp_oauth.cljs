(ns webapp.resources.setup.events.mcp-oauth
  "OAuth login flow for the MCP httpproxy subtype, used on the connection create
  page.

  When an admin creates an \"mcp\" connection whose endpoint is protected by
  OAuth (e.g. https://mcp.figma.com/mcp), the gateway acts as the OAuth client:
  it discovers the upstream authorization server, optionally registers a client
  dynamically, and drives an Authorization Code + PKCE login. The obtained
  access token is frozen into the connection's HEADER_AUTHORIZATION credential.

  The login is driven in a popup so the in-progress create form (held only in
  app-db) is preserved. The opener polls the popup's URL; once the gateway
  callback redirects it back to our own origin with ?mcp_oauth=...&flow_id=...,
  the opener reads the outcome, closes the popup, and redeems the token.

  State per role lives under [:resource-setup :roles role-index :mcp-oauth]:
    {:status      :idle | :authorizing | :pending | :success | :error
     :client-id   string  ;; optional, never written to connection env vars
     :client-secret string ;; optional, never written to connection env vars
     :error       string}

  The client-id/client-secret are auth-flow inputs only and deliberately kept
  out of the role :credentials map, because the httpproxy form turns every
  credential into a connection env var."
  (:require
   [clojure.string :as str]
   [re-frame.core :as rf]))

(def ^:private popup-timeout-ms (* 5 60 1000))
(def ^:private popup-poll-ms 500)

(defn- mcp-path [db role-index] [:resource-setup :roles role-index :mcp-oauth])

;; ---------------------------------------------------------------------------
;; Form-field events for the optional client credentials (kept out of :credentials)
;; ---------------------------------------------------------------------------

(rf/reg-event-db
 :mcp-oauth/set-field
 (fn [db [_ role-index field value]]
   (assoc-in db (conj (mcp-path db role-index) field) value)))

(rf/reg-sub
 :mcp-oauth/state
 (fn [db [_ role-index]]
   (get-in db [:resource-setup :roles role-index :mcp-oauth] {:status :idle})))

;; ---------------------------------------------------------------------------
;; Authorize: discover + (DCR) + build the auth URL, then open the popup
;; ---------------------------------------------------------------------------

(rf/reg-event-fx
 :mcp-oauth/authorize
 (fn [{:keys [db]} [_ role-index]]
   (let [creds (get-in db [:resource-setup :roles role-index :credentials] {})
         remote-url-raw (get creds "remote_url")
         server-url (str/trim (if (map? remote-url-raw) (:value remote-url-raw "") (or remote-url-raw "")))
         mcp-state (get-in db [:resource-setup :roles role-index :mcp-oauth] {})
         client-id (str/trim (or (:client-id mcp-state) ""))
         client-secret (str/trim (or (:client-secret mcp-state) ""))
         ;; Strip query/hash so the popup lands on a clean app URL the gateway
         ;; callback can redirect back to (validated against the API hostname).
         redirect (str (.. js/window -location -origin) (.. js/window -location -pathname))]
     (if (str/blank? server-url)
       {:fx [[:dispatch [:show-snackbar {:level :error
                                         :text "Enter the MCP server URL before authorizing"}]]]}
       {:db (assoc-in db (mcp-path db role-index)
                      (merge mcp-state {:status :authorizing :error nil}))
        :fx [[:dispatch [:fetch {:method "POST"
                                 :uri "/mcp-oauth/authorize"
                                 :query-params {:redirect redirect}
                                 :body (cond-> {:server_url server-url}
                                         (not (str/blank? client-id)) (assoc :client_id client-id)
                                         (not (str/blank? client-secret)) (assoc :client_secret client-secret))
                                 :on-success #(rf/dispatch [:mcp-oauth/authorize-success role-index %])
                                 :on-failure #(rf/dispatch [:mcp-oauth/authorize-failure role-index %])}]]]}))))

(rf/reg-event-fx
 :mcp-oauth/authorize-success
 (fn [{:keys [db]} [_ role-index response]]
   (let [auth-url (:authorization_url response)]
     (if (str/blank? auth-url)
       {:fx [[:dispatch [:mcp-oauth/authorize-failure role-index
                         {:message "Authorization URL was not returned"}]]]}
       {:db (assoc-in db (mcp-path db role-index)
                      (merge (get-in db [:resource-setup :roles role-index :mcp-oauth] {})
                             {:status :pending :error nil}))
        :fx [[:mcp-oauth/run-popup
              {:url auth-url
               :on-success [:mcp-oauth/fetch-token role-index]
               :on-error [:mcp-oauth/authorize-failure role-index]
               :on-cancel [:mcp-oauth/cancelled role-index]}]]}))))

(rf/reg-event-fx
 :mcp-oauth/authorize-failure
 (fn [{:keys [db]} [_ role-index error]]
   (let [reason (or (:message error) (get-in error [:body :message]) "Failed to start authorization")]
     {:db (assoc-in db (mcp-path db role-index)
                    (merge (get-in db [:resource-setup :roles role-index :mcp-oauth] {})
                           {:status :error :error reason}))
      :fx [[:dispatch [:show-snackbar {:level :error
                                       :text "MCP authorization failed"
                                       :details error}]]]})))

;; ---------------------------------------------------------------------------
;; Popup driver: opens the auth URL and polls for the same-origin callback
;; return. Lives in an effect because it touches window/timers directly.
;; ---------------------------------------------------------------------------

;; The popup driver is generic: callers pass re-frame event vectors that are
;; dispatched on outcome. :on-success gets the flow id appended; :on-error gets
;; an error map appended; :on-cancel is dispatched as-is. This lets both the
;; create flow (role-indexed) and the edit flow (single connection) reuse it.
(defn- handle-popup-return
  "Reads the same-origin callback return from the popup and dispatches the
  outcome. Returns true when a terminal outcome was handled (caller stops
  polling), false otherwise."
  [popup on-success on-error]
  (let [origin (.. js/window -location -origin)
        ;; Reading the popup location throws while it is on the provider's
        ;; (cross-origin) login page; that is expected and ignored until it
        ;; returns to our own origin.
        href (try (.. popup -location -href) (catch :default _ nil))]
    (if-not (and href (str/starts-with? href origin))
      false
      (let [params (js/URLSearchParams. (.. popup -location -search))
            outcome (.get params "mcp_oauth")]
        (if-not outcome
          false
          (let [flow-id (.get params "flow_id")
                reason (.get params "reason")]
            (.close popup)
            (if (= outcome "success")
              (rf/dispatch (conj (vec on-success) flow-id))
              (rf/dispatch (conj (vec on-error)
                                 {:message (str "Authorization denied"
                                                (when-not (str/blank? reason)
                                                  (str ": " (str/replace reason "_" " "))))})))
            true))))))

(rf/reg-fx
 :mcp-oauth/run-popup
 (fn [{:keys [url on-success on-error on-cancel]}]
   (let [popup (js/window.open url "hoop-mcp-oauth"
                               "width=600,height=800,menubar=no,toolbar=no,location=yes")]
     (if (nil? popup)
       (rf/dispatch (conj (vec on-error)
                          {:message "Popup blocked. Allow popups for this site and retry."}))
       (let [started (js/Date.now)
             timer (atom nil)
             stop! (fn [] (when @timer (js/clearInterval @timer)))]
         (reset! timer
                 (js/setInterval
                  (fn []
                    (cond
                      (.-closed popup)
                      (do (stop!)
                          (rf/dispatch on-cancel))

                      (> (- (js/Date.now) started) popup-timeout-ms)
                      (do (stop!)
                          (when-not (.-closed popup) (.close popup))
                          (rf/dispatch (conj (vec on-error)
                                             {:message "Authorization timed out"})))

                      :else
                      (when (handle-popup-return popup on-success on-error)
                        (stop!))))
                  popup-poll-ms)))))))

(rf/reg-event-db
 :mcp-oauth/cancelled
 (fn [db [_ role-index]]
   (assoc-in db (mcp-path db role-index)
             (merge (get-in db [:resource-setup :roles role-index :mcp-oauth] {})
                    {:status :idle :error nil}))))

;; ---------------------------------------------------------------------------
;; Redeem the token and freeze it into HEADER_AUTHORIZATION
;; ---------------------------------------------------------------------------

(rf/reg-event-fx
 :mcp-oauth/fetch-token
 (fn [{:keys [db]} [_ role-index flow-id]]
   (if (str/blank? flow-id)
     {:fx [[:dispatch [:mcp-oauth/authorize-failure role-index {:message "Missing flow id"}]]]}
     {:fx [[:dispatch [:fetch {:method "GET"
                               :uri (str "/mcp-oauth/token/" flow-id)
                               :on-success #(rf/dispatch [:mcp-oauth/fetch-token-success role-index %])
                               :on-failure #(rf/dispatch [:mcp-oauth/authorize-failure role-index %])}]]]})))

(rf/reg-event-fx
 :mcp-oauth/fetch-token-success
 (fn [{:keys [db]} [_ role-index response]]
   (let [auth-header (:authorization_header response)]
     (if (str/blank? auth-header)
       {:fx [[:dispatch [:mcp-oauth/authorize-failure role-index
                         {:message "No token returned"}]]]}
       {:db (assoc-in db (mcp-path db role-index)
                      (merge (get-in db [:resource-setup :roles role-index :mcp-oauth] {})
                             {:status :success
                              :error nil
                              :server-url (:server_url response)
                              :expires-in (:expires_in response)}))
        :fx [[:dispatch [:resource-setup->update-role-credentials role-index "HEADER_AUTHORIZATION" auth-header]]
             [:dispatch [:show-snackbar {:level :success
                                         :text "MCP connection authorized"}]]]}))))

(rf/reg-event-fx
 :mcp-oauth/clear
 (fn [{:keys [db]} [_ role-index]]
   {:db (-> db
            (assoc-in (mcp-path db role-index) {:status :idle})
            (update-in [:resource-setup :roles role-index :credentials] dissoc "HEADER_AUTHORIZATION"))}))
