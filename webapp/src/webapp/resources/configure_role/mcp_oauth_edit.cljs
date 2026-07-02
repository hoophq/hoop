(ns webapp.resources.configure-role.mcp-oauth-edit
  "OAuth login flow for the MCP httpproxy subtype on the connection EDIT screen.

  This mirrors webapp.resources.setup.events.mcp-oauth (the create flow) but
  reads/writes the edit flow's single-connection state under [:connection-setup]
  instead of the create flow's [:resource-setup :roles N]. The obtained access
  token is frozen into the connection's Authorization header, stored under
  [:connection-setup :network-credentials :HEADER_AUTHORIZATION] (NOT in the
  editable headers list) so it is managed by the Authorize widget rather than
  shown as a plain HTTP header row. process-form emits it on save, and the edit
  form moves any loaded AUTHORIZATION header into this slot on init.

  The popup driver itself (:mcp-oauth/run-popup) is shared and lives in the
  create-flow namespace; it is a global re-frame fx, so we just dispatch it with
  edit-flow callback vectors.

  State lives under [:connection-setup :mcp-oauth]:
    {:status :idle | :authorizing | :pending | :success | :error
     :client-id string :client-secret string :error string}
  client-id/client-secret are auth-flow inputs only and are never persisted as
  connection environment variables."
  (:require
   [clojure.string :as str]
   [re-frame.core :as rf]))

(def ^:private mcp-edit-path [:connection-setup :mcp-oauth])

(defn- unwrap [v]
  (if (map? v) (:value v "") (or v "")))

;; ---------------------------------------------------------------------------
;; State + optional client credential fields
;; ---------------------------------------------------------------------------

(rf/reg-sub
 :mcp-oauth/edit-state
 (fn [db _]
   (get-in db mcp-edit-path {:status :idle})))

(rf/reg-event-db
 :mcp-oauth/edit-set-field
 (fn [db [_ field value]]
   (assoc-in db (conj mcp-edit-path field) value)))

;; ---------------------------------------------------------------------------
;; Authorize: reuse the shared discovery/DCR/authorize endpoint + popup driver
;; ---------------------------------------------------------------------------

(rf/reg-event-fx
 :mcp-oauth/edit-authorize
 (fn [{:keys [db]} _]
   (let [server-url (str/trim (unwrap (get-in db [:connection-setup :network-credentials :remote_url])))
         mcp-state (get-in db mcp-edit-path {})
         client-id (str/trim (or (:client-id mcp-state) ""))
         client-secret (str/trim (or (:client-secret mcp-state) ""))
         redirect (str (.. js/window -location -origin) (.. js/window -location -pathname))]
     (if (str/blank? server-url)
       {:fx [[:dispatch [:show-snackbar {:level :error
                                         :text "Enter the MCP server URL before authorizing"}]]]}
       {:db (assoc-in db mcp-edit-path (merge mcp-state {:status :authorizing :error nil}))
        :fx [[:dispatch [:fetch {:method "POST"
                                 :uri "/mcp-oauth/authorize"
                                 :query-params {:redirect redirect}
                                 :body (cond-> {:server_url server-url}
                                         (not (str/blank? client-id)) (assoc :client_id client-id)
                                         (not (str/blank? client-secret)) (assoc :client_secret client-secret))
                                 :on-success #(rf/dispatch [:mcp-oauth/edit-authorize-success %])
                                 :on-failure #(rf/dispatch [:mcp-oauth/edit-authorize-failure %])}]]]}))))

(rf/reg-event-fx
 :mcp-oauth/edit-authorize-success
 (fn [{:keys [db]} [_ response]]
   (let [auth-url (:authorization_url response)]
     (if (str/blank? auth-url)
       {:fx [[:dispatch [:mcp-oauth/edit-authorize-failure {:message "Authorization URL was not returned"}]]]}
       {:db (assoc-in db mcp-edit-path (merge (get-in db mcp-edit-path {}) {:status :pending :error nil}))
        :fx [[:mcp-oauth/run-popup
              {:url auth-url
               :on-success [:mcp-oauth/edit-fetch-token]
               :on-error [:mcp-oauth/edit-authorize-failure]
               :on-cancel [:mcp-oauth/edit-cancelled]}]]}))))

(rf/reg-event-fx
 :mcp-oauth/edit-authorize-failure
 (fn [{:keys [db]} [_ error]]
   (let [reason (or (:message error) (get-in error [:body :message]) "Failed to start authorization")]
     {:db (assoc-in db mcp-edit-path (merge (get-in db mcp-edit-path {}) {:status :error :error reason}))
      :fx [[:dispatch [:show-snackbar {:level :error
                                       :text "MCP authorization failed"
                                       :details error}]]]})))

(rf/reg-event-db
 :mcp-oauth/edit-cancelled
 (fn [db _]
   (assoc-in db mcp-edit-path (merge (get-in db mcp-edit-path {}) {:status :idle :error nil}))))

;; ---------------------------------------------------------------------------
;; Redeem the token and freeze it into the Authorization header
;; ---------------------------------------------------------------------------

(rf/reg-event-fx
 :mcp-oauth/edit-fetch-token
 (fn [_ [_ flow-id]]
   (if (str/blank? flow-id)
     {:fx [[:dispatch [:mcp-oauth/edit-authorize-failure {:message "Missing flow id"}]]]}
     {:fx [[:dispatch [:fetch {:method "GET"
                               :uri (str "/mcp-oauth/token/" flow-id)
                               :on-success #(rf/dispatch [:mcp-oauth/edit-fetch-token-success %])
                               :on-failure #(rf/dispatch [:mcp-oauth/edit-authorize-failure %])}]]]})))

(rf/reg-event-fx
 :mcp-oauth/edit-fetch-token-success
 (fn [{:keys [db]} [_ response]]
   (let [auth-header (:authorization_header response)]
     (if (str/blank? auth-header)
       {:fx [[:dispatch [:mcp-oauth/edit-authorize-failure {:message "No token returned"}]]]}
       {:db (assoc-in db mcp-edit-path (merge (get-in db mcp-edit-path {})
                                              {:status :success :error nil}))
        :fx [[:dispatch [:connection-setup/update-network-credentials "HEADER_AUTHORIZATION" auth-header]]
             [:dispatch [:show-snackbar {:level :success
                                         :text "MCP connection authorized"}]]]}))))

(rf/reg-event-db
 :mcp-oauth/edit-clear
 (fn [db _]
   (-> db
       (update-in [:connection-setup :network-credentials] dissoc :HEADER_AUTHORIZATION)
       (assoc-in mcp-edit-path {:status :idle}))))
