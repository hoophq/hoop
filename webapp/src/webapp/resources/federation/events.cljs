(ns webapp.resources.federation.events
  (:require
   [clojure.string :as str]
   [re-frame.core :as rf]))

(def ^:private default-form
  {:enabled true
   :hook_source "builtin"
   :builtin_provider "gcp_iam"
   :admin_credentials_json ""
   :extra_config {:project_id ""}
   :identity_source_attribute "$.user.email"
   :identity_target_template "{user.email}"
   :fallback_policy "deny"
   :token_ttl_seconds 3600})

(defn- build-config [form]
  {:enabled (boolean (:enabled form))
   :hook_source "builtin"
   :builtin_provider (or (:builtin_provider form) "gcp_iam")
   :extra_config {:project_id (get-in form [:extra_config :project_id])}
   :identity_source_attribute (:identity_source_attribute form)
   :identity_target_template (:identity_target_template form)
   :fallback_policy (:fallback_policy form)
   :token_ttl_seconds (:token_ttl_seconds form)})

(rf/reg-event-fx
 :federation/load
 (fn [{:keys [db]} [_ connection-name]]
   {:db (update db :resources/federation merge {:status :loading
                                                :credentials-editing? false})
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/connections/" connection-name "/federation")
                             :on-success (fn [response]
                                           (rf/dispatch [:federation/load-success response]))
                             :on-failure (fn [error]
                                           (if (= 404 (:status error))
                                             (rf/dispatch [:federation/set-status :idle])
                                             (rf/dispatch [:federation/load-failure error])))}]]]}))

(rf/reg-event-fx
 :federation/load-success
 (fn [{:keys [db]} [_ response]]
   ;; The GET never echoes admin credentials back. For gcp_iam the same SA JSON
   ;; is also stored as the connection's static GOOGLE_APPLICATION_CREDENTIALS
   ;; secret, so we recover it from connection-setup to show the operator their
   ;; credentials. gcp_oauth stores OAuth client credentials that are never
   ;; mirrored into a static secret, so there is nothing to recover — the field
   ;; stays masked until the operator chooses to replace it.
   (let [provider (or (:builtin_provider response) "gcp_iam")
         config-files (get-in db [:connection-setup :credentials :configuration-files] [])
         sa-json (when (= provider "gcp_iam")
                   (some (fn [{:keys [key value]}]
                           (when (= key "GOOGLE_APPLICATION_CREDENTIALS")
                             (if (map? value) (:value value) value)))
                         config-files))
         form {:enabled (if (contains? response :enabled)
                          (boolean (:enabled response))
                          true)
               :hook_source (or (:hook_source response) "builtin")
               :builtin_provider provider
               :admin_credentials_json (or sa-json "")
               :extra_config {:project_id (get-in response [:extra_config :project_id] "")}
               :identity_source_attribute (or (:identity_source_attribute response) "$.user.email")
               :identity_target_template (or (:identity_target_template response) "{user.email}")
               :fallback_policy (or (:fallback_policy response) "deny")
               :token_ttl_seconds (or (:token_ttl_seconds response) 3600)}]
     {:db (update db :resources/federation merge {:status :ready
                                                   :data response
                                                   :form form})
      ;; A connection that already has federation configured should default
      ;; the edit-mode connection-method selector to "iam_federation".
      :fx [[:dispatch [:connection-setup/update-connection-method "iam_federation"]]]})))

(rf/reg-event-db
 :federation/load-failure
 (fn [db [_ error]]
   (update db :resources/federation merge {:status :error :error error})))

(rf/reg-event-db
 :federation/set-status
 (fn [db [_ status]]
   (assoc-in db [:resources/federation :status] status)))

(rf/reg-event-db
 :federation/set-field
 (fn [db [_ field value]]
   (assoc-in db [:resources/federation :form field] value)))

(rf/reg-event-db
 :federation/set-nested-field
 (fn [db [_ parent-field field value]]
   (assoc-in db [:resources/federation :form parent-field field] value)))

(rf/reg-event-db
 :federation/set-credentials-editing
 (fn [db [_ editing?]]
   (assoc-in db [:resources/federation :credentials-editing?] editing?)))

(rf/reg-event-db
 :federation/toggle-mapping-editor
 (fn [db _]
   (update-in db [:resources/federation :mapping-editor-open?] not)))

(rf/reg-event-fx
 :federation/save
 (fn [{:keys [db]} [_ connection-name]]
   (let [form (get-in db [:resources/federation :form])
         ;; omitting admin_credentials_json tells the backend to keep the stored value
         body (cond-> (build-config form)
                (not (str/blank? (:admin_credentials_json form)))
                (assoc :admin_credentials_json (:admin_credentials_json form)))]
     {:db (assoc-in db [:resources/federation :save-status] :loading)
      :fx [[:dispatch [:fetch {:method "PUT"
                               :uri (str "/connections/" connection-name "/federation")
                               :body body
                               :on-success (fn [response]
                                             (rf/dispatch [:federation/save-success response]))
                               :on-failure (fn [error]
                                             (rf/dispatch [:federation/save-failure error]))}]]]})))

(rf/reg-event-fx
 :federation/save-success
 (fn [{:keys [db]} [_ response]]
   {:db (update db :resources/federation merge
                {:status :ready
                 :save-status :ready
                 :data (assoc response :has_admin_credentials true)
                 :credentials-editing? false})
    :fx [[:dispatch [:show-snackbar {:level :success
                                     :text "IAM Federation configuration saved"}]]]}))

(rf/reg-event-fx
 :federation/save-failure
 (fn [{:keys [db]} [_ error]]
   {:db (assoc-in db [:resources/federation :save-status] :error)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to save IAM Federation configuration"
                                     :details error}]]]}))

(rf/reg-event-fx
 :federation/save-for-new-role
 (fn [{:keys [db]} [_ connection-name]]
   (let [form (get-in db [:resources/federation :form])
         body (assoc (build-config form) :admin_credentials_json (:admin_credentials_json form))]
     {:fx [[:dispatch [:fetch {:method "PUT"
                               :uri (str "/connections/" connection-name "/federation")
                               :body body
                               :on-success (fn [_]
                                             (rf/dispatch [:resources->federation-roles-saved]))
                               :on-failure (fn [error]
                                             (rf/dispatch [:resources->federation-rollback nil error]))}]]]})))

(rf/reg-event-fx
 :federation/delete
 (fn [{:keys [db]} [_ connection-name]]
   {:db (assoc-in db [:resources/federation :save-status] :loading)
    :fx [[:dispatch [:fetch {:method "DELETE"
                             :uri (str "/connections/" connection-name "/federation")
                             :on-success (fn []
                                           (rf/dispatch [:federation/delete-success]))
                             :on-failure (fn [error]
                                           (rf/dispatch [:federation/save-failure error]))}]]]}))

(rf/reg-event-db
 :federation/delete-success
 (fn [db _]
   (update db :resources/federation merge {:status :idle
                                           :data nil
                                           :save-status :idle
                                           :mapping-editor-open? false
                                           :form default-form})))

;; gcp_oauth per-user consent flow. Each user connects their own Google
;; account to a connection: the gateway returns a Google consent URL, the
;; browser is sent there, and Google redirects back to the gateway callback
;; (which stores the refresh token) and then back to this page with a
;; ?federation_oauth=success|error outcome that :federation/handle-oauth-outcome
;; turns into a snackbar.
(rf/reg-event-fx
 :federation/oauth-connect
 (fn [_ [_ connection-name]]
   (let [redirect (.. js/window -location -href)]
     {:fx [[:dispatch [:fetch {:method "GET"
                               :uri (str "/connections/" connection-name "/federation/oauth/authorize")
                               :query-params {:redirect redirect}
                               :on-success (fn [response]
                                             (when-let [authorize-url (:url response)]
                                               (set! js/window.location.href authorize-url)))
                               :on-failure (fn [error]
                                             (rf/dispatch [:show-snackbar {:level :error
                                                                           :text "Failed to start Google authorization"
                                                                           :details error}]))}]]]})))

(rf/reg-event-fx
 :federation/oauth-disconnect
 (fn [_ [_ connection-name]]
   {:fx [[:dispatch [:fetch {:method "DELETE"
                             :uri (str "/connections/" connection-name "/federation/oauth")
                             :on-success (fn [_]
                                           (rf/dispatch [:show-snackbar {:level :success
                                                                         :text "Google account disconnected"}]))
                             :on-failure (fn [error]
                                           (if (= 404 (:status error))
                                             (rf/dispatch [:show-snackbar {:level :info
                                                                           :text "No Google account was connected"}])
                                             (rf/dispatch [:show-snackbar {:level :error
                                                                           :text "Failed to disconnect Google account"
                                                                           :details error}])))}]]]}))

;; Per-user OAuth connection status, keyed by connection name. Lets end-user
;; surfaces (the webclient) know whether the signed-in user has connected their
;; Google account to a gcp_oauth connection so they can be prompted before
;; running. The status endpoint is access-scoped and gcp_oauth-gated on the
;; backend; for non-federated or non-oauth connections it returns provider="".
(rf/reg-event-fx
 :federation/oauth-status
 (fn [_ [_ connection-name]]
   (when-not (str/blank? connection-name)
     {:fx [[:dispatch [:fetch {:method "GET"
                               :uri (str "/connections/" connection-name "/federation/oauth")
                               :on-success (fn [response]
                                             (rf/dispatch [:federation/set-oauth-status connection-name response]))
                               ;; A 404 means the connection isn't visible/federated for
                               ;; this user; treat it as "nothing to connect".
                               :on-failure (fn [_]
                                             (rf/dispatch [:federation/set-oauth-status connection-name nil]))}]]]})))

(rf/reg-event-db
 :federation/set-oauth-status
 (fn [db [_ connection-name status]]
   (assoc-in db [:resources/federation :oauth-status connection-name]
             {:provider (:provider status)
              :connected (boolean (:connected status))
              :google_email (:google_email status)})))

;; Reads the ?federation_oauth=success|error&reason=... the OAuth callback
;; appends when redirecting the browser back to the app. Shows the outcome as a
;; snackbar, refreshes the connection's status (so a freshly-connected account
;; clears any "connect" prompt), then strips the params so a refresh doesn't
;; replay the toast. Safe to dispatch on any page mount: it is a no-op when the
;; params are absent.
(rf/reg-event-fx
 :federation/consume-oauth-return
 (fn [_ _]
   (let [params (js/URLSearchParams. (.. js/window -location -search))
         outcome (.get params "federation_oauth")]
     (if (str/blank? outcome)
       {}
       (let [reason (.get params "reason")
             role (.get params "role")
             url (js/URL. (.. js/window -location -href))]
         (.delete (.-searchParams url) "federation_oauth")
         (.delete (.-searchParams url) "reason")
         (.replaceState js/history nil "" (.toString url))
         {:fx (cond-> [[:dispatch [:federation/handle-oauth-outcome outcome reason]]]
                (and (= outcome "success") (not (str/blank? role)))
                (conj [:dispatch [:federation/oauth-status role]]))})))))

(rf/reg-event-fx
 :federation/handle-oauth-outcome
 (fn [_ [_ outcome reason]]
   {:fx [[:dispatch
          (case outcome
            "success" [:show-snackbar {:level :success
                                       :text "Google account connected"}]
            "error" [:show-snackbar {:level :error
                                     :text (str "Google authorization failed"
                                                (when-not (str/blank? reason)
                                                  (str ": " (str/replace reason "_" " "))))}]
            [:show-snackbar {:level :info :text "Google authorization finished"}])]]}))

(rf/reg-event-fx
 :federation/test
 (fn [{:keys [db]} [_ user-email conn-data]]
   (let [form (get-in db [:resources/federation :form])
         project-id (get-in form [:extra_config :project_id])
         payload {:user_email user-email
                  :config (assoc (build-config form)
                                 :admin_credentials_json (:admin_credentials_json form))
                  :connection {:agent_id (:agent_id conn-data)
                               :type (or (:type conn-data) "database")
                               :subtype (or (:subtype conn-data) "bigquery")
                               :command ["bq" "query" "--use_legacy_sql=false" "--format=pretty"]
                               :test_script "SELECT 1"
                               :envs (merge (or (:envs conn-data) {})
                                            (when project-id
                                              {:CLOUDSDK_CORE_PROJECT project-id}))}}]
     {:db (update db :resources/federation merge {:test-status :loading
                                                  :test-result nil})
      :fx [[:dispatch [:fetch {:method "POST"
                               :uri "/federation/test"
                               :body payload
                               :on-success (fn [response]
                                             (rf/dispatch [:federation/test-success response]))
                               :on-failure (fn [error]
                                             (rf/dispatch [:federation/test-failure error]))}]]]})))

(rf/reg-event-db
 :federation/test-success
 (fn [db [_ response]]
   ;; The /federation/test endpoint returns HTTP 200 even when the test
   ;; itself fails — the actual outcome lives in the response body's
   ;; :success field. Inspect it to drive UI status.
   (update db :resources/federation merge
           {:test-status (if (:success response) :success :error)
            :test-result response})))

(rf/reg-event-db
 :federation/test-failure
 (fn [db [_ error]]
   (update db :resources/federation merge {:test-status :error :test-result error})))

(rf/reg-event-db
 :federation/reset-test
 (fn [db _]
   (update db :resources/federation merge {:test-status :idle :test-result nil})))

(rf/reg-event-db
 :federation/clear
 (fn [db _]
   (assoc db :resources/federation
          {:status :idle
           :data nil
           :form default-form
           :credentials-editing? false
           :mapping-editor-open? false
           :test-status :idle
           :test-result nil})))
