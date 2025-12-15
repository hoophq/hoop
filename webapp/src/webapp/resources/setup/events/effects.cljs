(ns webapp.resources.setup.events.effects
  (:require
   [clojure.string :as str]
   [re-frame.core :as rf]
   [webapp.resources.setup.events.process-form :as process-form]
   [webapp.resources.helpers :as helpers]))

(rf/reg-event-db
 :resource-setup->initialize-state
 (fn [db [_ initial-data]]
   (if initial-data
     (assoc db :resource-setup initial-data)
     (assoc db :resource-setup {:current-step :resource-name
                                :roles []}))))

(rf/reg-event-fx
 :resource-setup->initialize-from-catalog
 (fn [{:keys [db]} [_ {:keys [type subtype command]}]]
   {:db (update db :resource-setup merge {:type type
                                          :subtype subtype
                                          :command command
                                          :current-step :resource-name
                                          :from-catalog? true
                                          :name ""
                                          :agent-id nil
                                          :roles []})
    :fx []}))

(rf/reg-event-db
 :resource-setup->set-resource-name
 (fn [db [_ name]]
   (assoc-in db [:resource-setup :name] name)))

;; Validate resource name (check if it already exists)
(rf/reg-event-fx
 :resource-setup->validate-resource-name
 (fn [_ [_ resource-name on-success]]
   {:fx [[:dispatch
          [:fetch {:method "GET"
                   :uri (str "/resources/" resource-name)
                   :on-success (fn [_resource]
                                 ;; Resource exists - name is taken
                                 (rf/dispatch [:resource-setup->set-name-validation-result false]))
                   :on-failure (fn [error]
                                 ;; Check if it's a 404 (resource not found) - name is available
                                 (if (= (:status error) 404)
                                   (do
                                     (rf/dispatch [:resource-setup->set-name-validation-result true])
                                     (when on-success
                                       (rf/dispatch on-success)))
                                   ;; Other errors - show error message
                                   (do
                                     (rf/dispatch [:resource-setup->set-name-validation-result nil])
                                     (rf/dispatch [:show-snackbar
                                                   {:level :error
                                                    :text "Failed to validate resource name"}]))))}]]]}))

(rf/reg-event-fx
 :resource-setup->set-name-validation-result
 (fn [_ [_ is-available?]]
   {:fx (when (false? is-available?)
          [[:dispatch [:show-snackbar
                       {:level :error
                        :text "This resource name is already in use. Please choose a different name."}]]])}))

(rf/reg-event-db
 :resource-setup->set-agent-id
 (fn [db [_ agent-id]]
   (assoc-in db [:resource-setup :agent-id] agent-id)))

(rf/reg-event-db
 :resource-setup->set-agent-creation-mode
 (fn [db [_ mode]]
   (assoc-in db [:resource-setup :agent-creation-mode] mode)))

;; Fetch agent ID by name after creation
(rf/reg-event-fx
 :resource-setup->fetch-agent-id-by-name
 (fn [_ [_ agent-name]]
   {:fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/agents"
                      :on-success (fn [agents]
                                    (let [created-agent (->> agents
                                                             (filter #(= (:name %) agent-name))
                                                             first)
                                          agent-id (:id created-agent)]
                                      (rf/dispatch [:resource-setup->set-agent-id agent-id])
                                      (rf/dispatch [:webapp.events.agents/set-agents agents])))
                      :on-failure (fn [error]
                                    (rf/dispatch [:show-snackbar {:level :error
                                                                  :text "Failed to fetch agents"
                                                                  :details error}]))}]]]}))


(rf/reg-event-fx
 :resource-setup->clear-agent-state
 (fn [{:keys [db]} _]
   {:db (-> db
            (assoc-in [:resource-setup :agent-id] nil)
            (assoc-in [:resource-setup :agent-creation-mode] nil))}))

;; Role management
(rf/reg-event-db
 :resource-setup->add-role
 (fn [db [_]]
   (let [resource-type (get-in db [:resource-setup :type])
         resource-subtype (get-in db [:resource-setup :subtype])
         resource-name (get-in db [:resource-setup :name])
         command (get-in db [:resource-setup :command])
         new-role {:name (helpers/random-role-name resource-name)
                   :type resource-type
                   :subtype resource-subtype
                   :command command
                   :connection-method "manual-input"
                   :credentials {}
                   :environment-variables []
                   :configuration-files []}]
     (update-in db [:resource-setup :roles] (fnil conj []) new-role))))

(rf/reg-event-db
 :resource-setup->remove-role
 (fn [db [_ role-index]]
   (update-in db [:resource-setup :roles]
              (fn [roles]
                (vec (concat (subvec roles 0 role-index)
                             (subvec roles (inc role-index))))))))

(rf/reg-event-db
 :resource-setup->update-role-name
 (fn [db [_ role-index name]]
   (assoc-in db [:resource-setup :roles role-index :name] name)))

(rf/reg-event-db
 :resource-setup->update-role-connection-method
 (fn [db [_ role-index method]]
   (let [resource-subtype (get-in db [:resource-setup :subtype])
         supports-aws-iam? (contains? #{"mysql" "postgres"} resource-subtype)
         updated-db (assoc-in db [:resource-setup :roles role-index :connection-method] method)]
     ;; When switching to AWS IAM Role for MySQL/PostgreSQL, auto-set pass field to "authtoken" (hidden, without prefix)
     (if (and (= method "aws-iam-role") supports-aws-iam?)
       (let [metadata-credentials (get-in updated-db [:resource-setup :roles role-index :metadata-credentials] {})
             ;; Find pass field (case-insensitive - could be "pass" or "PASS")
             pass-key (or (first (filter #(= (str/lower-case %) "pass") (keys metadata-credentials)))
                          "PASS")
             pass-value (get metadata-credentials pass-key)
             ;; Auto-set pass field to "authtoken" (without prefix - prefix applied on submit, field is hidden)
             updated-metadata-credentials (if (or (nil? pass-value)
                                                  (str/blank? (if (map? pass-value) (:value pass-value) pass-value)))
                                            (assoc metadata-credentials pass-key {:value "authtoken" :prefix ""})
                                            metadata-credentials)]
         ;; Don't prefill user field - leave it empty for user to fill
         (assoc-in updated-db [:resource-setup :roles role-index :metadata-credentials] updated-metadata-credentials))
       updated-db))))

(rf/reg-event-db
 :resource-setup->update-role-credentials
 (fn [db [_ role-index key value prefix]]
   (if (or (boolean? value) (nil? prefix))
     (assoc-in db [:resource-setup :roles role-index :credentials key] value)
     (let [current-value (get-in db [:resource-setup :roles role-index :credentials key])
           existing-prefix (if (map? current-value)
                             (:prefix current-value)
                             prefix)
           new-value {:value value :prefix existing-prefix}]
       (assoc-in db [:resource-setup :roles role-index :credentials key] new-value)))))

(rf/reg-event-db
 :resource-setup->update-role-metadata-credentials
 (fn [db [_ role-index key value prefix]]
   (let [current-value (get-in db [:resource-setup :roles role-index :metadata-credentials key])
         existing-prefix (if (map? current-value)
                           (:prefix current-value)
                           (or prefix ""))
         new-value {:value value :prefix existing-prefix}]
     (assoc-in db [:resource-setup :roles role-index :metadata-credentials key] new-value))))

(rf/reg-event-db
 :resource-setup->update-secrets-manager-provider
 (fn [db [_ role-index provider]]
   (let [new-prefix (helpers/get-secret-prefix provider)
         ;; Update all metadata-credentials prefixes
         metadata-credentials (get-in db [:resource-setup :roles role-index :metadata-credentials] {})
         updated-metadata-credentials (reduce-kv (fn [acc k v]
                                                   (assoc acc k (assoc v :prefix new-prefix)))
                                                 {}
                                                 metadata-credentials)
         credentials (get-in db [:resource-setup :roles role-index :credentials] {})
         updated-credentials (reduce-kv (fn [acc k v]
                                          (assoc acc k (assoc v :prefix new-prefix)))
                                        {}
                                        credentials)
         ;; Update all config-files prefixes
         config-files (get-in db [:resource-setup :roles role-index :configuration-files] [])
         updated-config-files (mapv (fn [file]
                                      (update file :value assoc :prefix new-prefix))
                                    config-files)
         current-role (get-in db [:resource-setup :roles role-index])
         updated-role (merge current-role
                             {:secrets-manager-provider provider
                              :metadata-credentials updated-metadata-credentials
                              :credentials updated-credentials
                              :configuration-files updated-config-files})]
     (assoc-in db [:resource-setup :roles role-index] updated-role))))

(rf/reg-event-db
 :resource-setup->update-field-source
 (fn [db [_ role-index field-key source]]
   (let [new-prefix (helpers/get-secret-prefix source)
         ;; Update metadata-credentials if exists
         metadata-credentials (get-in db [:resource-setup :roles role-index :metadata-credentials] {})
         metadata-value (get metadata-credentials field-key)
         updated-metadata-credentials (if metadata-value
                                        (assoc metadata-credentials field-key
                                               (assoc metadata-value :prefix new-prefix))
                                        metadata-credentials)
         ;; Update credentials if exists
         credentials (get-in db [:resource-setup :roles role-index :credentials] {})
         credential-value (get credentials field-key)
         updated-credentials (if credential-value
                               (assoc credentials field-key
                                      (assoc credential-value :prefix new-prefix))
                               credentials)
         current-role (get-in db [:resource-setup :roles role-index])
         current-field-sources (get current-role :field-sources {})
         updated-field-sources (assoc current-field-sources field-key source)
         updated-role (merge current-role
                             {:field-sources updated-field-sources
                              :metadata-credentials updated-metadata-credentials
                              :credentials updated-credentials})]
     (assoc-in db [:resource-setup :roles role-index] updated-role))))

;; Environment variables for roles - New pattern with current-key/current-value
(rf/reg-event-db
 :resource-setup->update-role-env-current-key
 (fn [db [_ role-index value]]
   (assoc-in db [:resource-setup :roles role-index :env-current-key] value)))

(rf/reg-event-db
 :resource-setup->update-role-env-current-value
 (fn [db [_ role-index value]]
   (assoc-in db [:resource-setup :roles role-index :env-current-value] value)))

(rf/reg-event-db
 :resource-setup->add-role-env-row
 (fn [db [_ role-index]]
   (let [current-key (get-in db [:resource-setup :roles role-index :env-current-key])
         current-value (get-in db [:resource-setup :roles role-index :env-current-value])]
     (if (and (not (str/blank? current-key)) (not (str/blank? current-value)))
       (update-in db [:resource-setup :roles role-index]
                  #(-> %
                       (update :environment-variables (fnil conj [])
                               {:key current-key
                                :value current-value})
                       (merge {:env-current-key "" :env-current-value ""})))
       db))))

(rf/reg-event-db
 :resource-setup->update-role-env-var
 (fn [db [_ role-index var-index field value]]
   (assoc-in db [:resource-setup :roles role-index :environment-variables var-index field] value)))

;; Configuration files for roles - New pattern with current-name/current-content
(rf/reg-event-db
 :resource-setup->update-role-config-current-name
 (fn [db [_ role-index value]]
   (assoc-in db [:resource-setup :roles role-index :config-current-name] value)))

(rf/reg-event-db
 :resource-setup->update-role-config-current-content
 (fn [db [_ role-index value]]
   (assoc-in db [:resource-setup :roles role-index :config-current-content] value)))

(rf/reg-event-db
 :resource-setup->add-role-config-row
 (fn [db [_ role-index]]
   (let [current-name (get-in db [:resource-setup :roles role-index :config-current-name])
         current-content (get-in db [:resource-setup :roles role-index :config-current-content])]
     (if (and (not (str/blank? current-name)) (not (str/blank? current-content)))
       (update-in db [:resource-setup :roles role-index]
                  #(-> %
                       (update :configuration-files (fnil conj [])
                               {:key current-name
                                :value current-content})
                       (merge {:config-current-name "" :config-current-content ""})))
       db))))

(rf/reg-event-db
 :resource-setup->update-role-config-file
 (fn [db [_ role-index file-index field value]]
   (assoc-in db [:resource-setup :roles role-index :configuration-files file-index field] value)))

(rf/reg-event-db
 :resource-setup->update-role-config-file-by-key
 (fn [db [_ role-index file-key value prefix]]
   (let [config-files (get-in db [:resource-setup :roles role-index :configuration-files] [])
         existing-index (first (keep-indexed (fn [idx {:keys [key]}]
                                               (when (= key file-key) idx))
                                             config-files))
         existing-file (when existing-index (get config-files existing-index))
         existing-prefix (if existing-file
                           (get-in existing-file [:value :prefix] "")
                           (or prefix ""))
         new-file-value {:value value :prefix existing-prefix}]
     (if existing-index
       (assoc-in db [:resource-setup :roles role-index :configuration-files existing-index :value] new-file-value)
       (update-in db [:resource-setup :roles role-index :configuration-files]
                  (fnil conj [])
                  {:key file-key :value new-file-value})))))

(rf/reg-event-db
 :resource-setup->set-role-command-args
 (fn [db [_ role-index args]]
   (assoc-in db [:resource-setup :roles role-index :command-args] args)))

(rf/reg-event-db
 :resource-setup->set-role-command-current-arg
 (fn [db [_ role-index arg]]
   (assoc-in db [:resource-setup :roles role-index :command-current-arg] arg)))

;; Submit
(rf/reg-event-fx
 :resource-setup->submit
 (fn [{:keys [db]} _]
   (let [payload (process-form/process-payload db)
         ;; Armazena as roles processadas para usar no success step
         processed-roles (:roles payload)]
     {:db (assoc-in db [:resource-setup :processed-roles] processed-roles)
      :fx [[:dispatch [:resources->create-resource payload]]]})))

;; Navigation helpers
(rf/reg-event-fx
 :resource-setup->next-step
 (fn [{:keys [db]} [_ next-step]]
   {:db (assoc-in db [:resource-setup :current-step] next-step)}))

(rf/reg-event-fx
 :resource-setup->back
 (fn [{:keys [db]} _]
   (let [current-step (get-in db [:resource-setup :current-step])]
     {:db (assoc-in db [:resource-setup :current-step]
                    (case current-step
                      :agent-selector :resource-name
                      :roles :agent-selector
                      :success :roles
                      :resource-name))})))

