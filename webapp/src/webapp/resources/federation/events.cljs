(ns webapp.resources.federation.events
  (:require
   [re-frame.core :as rf]))

(def ^:private default-form
  {:enabled true
   :hook_source "builtin"
   :builtin_provider "gcp_iam"
   :admin_credentials_json ""
   :extra_config {:project_id ""}
   :identity_source_attribute "$.user.email"
   :identity_target_template "{user.email}"
   :fallback "deny"
   :token_ttl_seconds 3600})

(defn- build-config [form]
  {:enabled (boolean (:enabled form))
   :hook_source "builtin"
   :builtin_provider "gcp_iam"
   :extra_config {:project_id (get-in form [:extra_config :project_id])}
   :identity_source_attribute (:identity_source_attribute form)
   :identity_target_template (:identity_target_template form)
   :fallback (:fallback form)
   :token_ttl_seconds (:token_ttl_seconds form)})

(rf/reg-event-fx
 :federation/load
 (fn [{:keys [db]} [_ connection-name]]
   {:db (-> db
            (assoc-in [:resources/federation :status] :loading)
            (assoc-in [:resources/federation :credentials-editing?] false))
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
   (let [form {:enabled (if (contains? response :enabled)
                          (boolean (:enabled response))
                          true)
               :hook_source (or (:hook_source response) "builtin")
               :builtin_provider (or (:builtin_provider response) "gcp_iam")
               :admin_credentials_json ""
               :extra_config {:project_id (get-in response [:extra_config :project_id] "")}
               :identity_source_attribute (or (:identity_source_attribute response) "$.user.email")
               :identity_target_template (or (:identity_target_template response) "{user.email}")
               :fallback (or (:fallback response) "deny")
               :token_ttl_seconds (or (:token_ttl_seconds response) 3600)}]
     {:db (-> db
              (assoc-in [:resources/federation :status] :ready)
              (assoc-in [:resources/federation :data] response)
              (assoc-in [:resources/federation :form] form))
      ;; A connection that already has federation configured should default
      ;; the edit-mode connection-method selector to "iam_federation".
      :fx [[:dispatch [:connection-setup/update-connection-method "iam_federation"]]]})))

(rf/reg-event-db
 :federation/load-failure
 (fn [db [_ error]]
   (-> db
       (assoc-in [:resources/federation :status] :error)
       (assoc-in [:resources/federation :error] error))))

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
         credentials-editing? (get-in db [:resources/federation :credentials-editing?])
         has-credentials? (get-in db [:resources/federation :data :has_admin_credentials])
         body (cond-> (build-config form)
                (or credentials-editing? (not has-credentials?))
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
   {:db (-> db
            (assoc-in [:resources/federation :status] :ready)
            (assoc-in [:resources/federation :save-status] :ready)
            (assoc-in [:resources/federation :data] (assoc response :has_admin_credentials true))
            (assoc-in [:resources/federation :credentials-editing?] false))
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
   (-> db
       (assoc-in [:resources/federation :status] :idle)
       (assoc-in [:resources/federation :data] nil)
       (assoc-in [:resources/federation :save-status] :idle)
       (assoc-in [:resources/federation :mapping-editor-open?] false)
       (assoc-in [:resources/federation :form] default-form))))

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
     {:db (-> db
              (assoc-in [:resources/federation :test-status] :loading)
              (assoc-in [:resources/federation :test-result] nil))
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
   (-> db
       (assoc-in [:resources/federation :test-status]
                 (if (:success response) :success :error))
       (assoc-in [:resources/federation :test-result] response))))

(rf/reg-event-db
 :federation/test-failure
 (fn [db [_ error]]
   (-> db
       (assoc-in [:resources/federation :test-status] :error)
       (assoc-in [:resources/federation :test-result] error))))

(rf/reg-event-db
 :federation/reset-test
 (fn [db _]
   (-> db
       (assoc-in [:resources/federation :test-status] :idle)
       (assoc-in [:resources/federation :test-result] nil))))

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
