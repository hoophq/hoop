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
   :builtin_provider "gcp_iam"
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
   ;; The GET never echoes admin credentials back, but the same SA JSON is
   ;; stored as the connection's static GOOGLE_APPLICATION_CREDENTIALS secret —
   ;; recover it from connection-setup so the operator sees their credentials.
   (let [config-files (get-in db [:connection-setup :credentials :configuration-files] [])
         sa-json (some (fn [{:keys [key value]}]
                         (when (= key "GOOGLE_APPLICATION_CREDENTIALS")
                           (if (map? value) (:value value) value)))
                       config-files)
         form {:enabled (if (contains? response :enabled)
                          (boolean (:enabled response))
                          true)
               :hook_source (or (:hook_source response) "builtin")
               :builtin_provider (or (:builtin_provider response) "gcp_iam")
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
