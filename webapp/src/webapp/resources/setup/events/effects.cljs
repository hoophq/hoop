(ns webapp.resources.setup.events.effects
  (:require
   [clojure.string :as str]
   [re-frame.core :as rf]
   [webapp.resources.setup.events.process-form :as process-form]
   [webapp.resources.helpers :as helpers]
   [webapp.connections.views.setup.connection-method :as connection-method]))

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

;; Role helper functions
(defn update-role-credentials-source
  "Updates all credentials to use the given source, preserving values."
  [role source]
  (-> role
      (update :metadata-credentials
              #(update-vals (or % {})
                            (fn [v]
                              (let [raw-value (if (map? v) (:value v) v)
                                    ;; Normalize to strip any existing prefix
                                    normalized (connection-method/normalize-credential-value raw-value)]
                                {:value (:value normalized)
                                 :source source}))))
      (update :credentials
              #(reduce-kv (fn [m k v]
                            (if (= k "insecure")
                              (assoc m k v)
                              (let [raw-value (if (map? v) (:value v) v)
                                    ;; Normalize to strip any existing prefix
                                    normalized (connection-method/normalize-credential-value raw-value)]
                                (assoc m k {:value (:value normalized)
                                            :source source}))))
                          {}
                          (or % {})))))

(defn update-role-secrets-manager-provider
  "Updates the secrets manager provider and all credentials sources."
  [role provider]
  (let [secrets-providers #{"vault-kv1" "vault-kv2" "aws-secrets-manager"}
        connection-method (:connection-method role "manual-input")
        is-secrets-manager? (= connection-method "secrets-manager")
        update-env-var-source (fn [env-var]
                                (let [value-map (:value env-var)
                                      current-source (when (map? value-map) (:source value-map))
                                      should-update? (or (contains? secrets-providers current-source)
                                                         (and is-secrets-manager?
                                                              (not= current-source "manual-input")))]
                                  (if should-update?
                                    (let [value-str (if (map? value-map)
                                                      (:value value-map)
                                                      (str value-map))
                                          ;; Normalize to strip any existing prefix from the value string
                                          normalized (connection-method/normalize-credential-value value-str)]
                                      (assoc env-var :value
                                             {:value (:value normalized) :source provider}))
                                    env-var)))
        update-env-current-value (fn [v]
                                   (let [current-source (when (map? v) (:source v))
                                         should-update? (or (contains? secrets-providers current-source)
                                                            (and is-secrets-manager?
                                                                 (not= current-source "manual-input")))]
                                     (if should-update?
                                       (let [raw-value (if (map? v)
                                                         (:value v)
                                                         (str v))
                                             ;; Normalize to strip any existing prefix
                                             normalized (connection-method/normalize-credential-value raw-value)]
                                         {:value (:value normalized) :source provider})
                                       v)))]
    (-> role
        (assoc :secrets-manager-provider provider)
        (update-role-credentials-source provider)
        (update :environment-variables
                (fn [env-vars]
                  (mapv update-env-var-source (or env-vars []))))
        (update :env-current-value update-env-current-value))))

(defn update-field-source-if-present
  "Updates the source for a field in a credentials map, preserving the value."
  [m field-key source]
  (if (= field-key "insecure")
    m
    (update m field-key
            (fn [v]
              {:value (if (map? v)
                        (:value v)
                        (or v ""))
               :source source}))))

(defn update-role-field-source
  "Updates the source for a field in both metadata-credentials and credentials."
  [role field-key source]
  (-> role
      (update :metadata-credentials
              #(update-field-source-if-present (or % {}) field-key source))
      (update :credentials
              #(update-field-source-if-present (or % {}) field-key source))))

(rf/reg-event-db
 :resource-setup->update-role-connection-method
 (fn [db [_ role-index method]]
   (let [current-provider (get-in db [:resource-setup :roles role-index :secrets-manager-provider])
         provider (if (str/blank? current-provider) "vault-kv1" current-provider)]
     (update-in db [:resource-setup :roles role-index]
                (fn [role]
                  (if (= method "secrets-manager")
                    (-> role
                        (assoc :connection-method method)
                        (update-role-secrets-manager-provider provider))
                    (-> role
                        (assoc :connection-method method)
                        (update-role-credentials-source method))))))))

(rf/reg-event-db
 :resource-setup->update-role-credentials
 (fn [db [_ role-index key value]]
   ;; "insecure" flag should always be stored as a boolean, never wrapped in a map
   (if (= key "insecure")
     (assoc-in db [:resource-setup :roles role-index :credentials key] value)
     (let [current-value (get-in db [:resource-setup :roles role-index :credentials key])
           connection-method (get-in db [:resource-setup :roles role-index :connection-method] "manual-input")
           secrets-provider (get-in db [:resource-setup :roles role-index :secrets-manager-provider] "vault-kv1")
           existing-source (when (map? current-value) (:source current-value))
           default-source (if (= connection-method "secrets-manager")
                            secrets-provider
                            "manual-input")
           new-source (or existing-source default-source)
           new-value (if (or (map? current-value) (= connection-method "secrets-manager"))
                       {:value value :source new-source}
                       value)]
       (assoc-in db [:resource-setup :roles role-index :credentials key] new-value)))))

(rf/reg-event-db
 :resource-setup->update-role-metadata-credentials
 (fn [db [_ role-index key value source]]
   (let [current-value (get-in db [:resource-setup :roles role-index :metadata-credentials key])
         connection-method (get-in db [:resource-setup :roles role-index :connection-method] "manual-input")
         secrets-provider (get-in db [:resource-setup :roles role-index :secrets-manager-provider] "vault-kv1")
         existing-source (when (map? current-value) (:source current-value))
         inferred-source (or source
                             existing-source
                             (when (= connection-method "secrets-manager") secrets-provider)
                             "manual-input")
         new-value {:value value :source inferred-source}]
     (assoc-in db [:resource-setup :roles role-index :metadata-credentials key] new-value))))

(rf/reg-event-db
 :resource-setup->update-secrets-manager-provider
 (fn [db [_ role-index provider]]
   (update-in db [:resource-setup :roles role-index]
              update-role-secrets-manager-provider
              provider)))

(rf/reg-event-db
 :resource-setup->update-field-source
 (fn [db [_ role-index field-key source]]
   (if (str/blank? source)
     db
     (let [is-secrets-provider? (contains? #{"vault-kv1" "vault-kv2" "aws-secrets-manager"} source)
           updated-db (update-in db
                                 [:resource-setup :roles role-index]
                                 update-role-field-source
                                 field-key
                                 source)]
       (if (and is-secrets-provider?
                (not= (get-in updated-db [:resource-setup :roles role-index :secrets-manager-provider]) source))
         (update-in updated-db
                    [:resource-setup :roles role-index]
                    #(update-role-secrets-manager-provider % source))
         updated-db)))))

;; Environment variables for roles - New pattern with current-key/current-value
(rf/reg-event-db
 :resource-setup->update-role-env-current-key
 (fn [db [_ role-index value]]
   (assoc-in db [:resource-setup :roles role-index :env-current-key] value)))

(rf/reg-event-db
 :resource-setup->update-role-env-current-value
 (fn [db [_ role-index value]]
   (let [existing-value (get-in db [:resource-setup :roles role-index :env-current-value])
         existing-source (when (map? existing-value) (:source existing-value))
         connection-method (get-in db [:resource-setup :roles role-index :connection-method] "manual-input")
         secrets-provider (get-in db [:resource-setup :roles role-index :secrets-manager-provider] "vault-kv1")
         inferred-source (or existing-source
                             (when (= connection-method "secrets-manager") secrets-provider)
                             "manual-input")
         new-value {:value (str value) :source inferred-source}]
     (assoc-in db [:resource-setup :roles role-index :env-current-value] new-value))))

(rf/reg-event-db
 :resource-setup->add-role-env-row
 (fn [db [_ role-index]]
   (let [current-key (get-in db [:resource-setup :roles role-index :env-current-key])
         current-value-map (get-in db [:resource-setup :roles role-index :env-current-value])
         current-value (if (map? current-value-map) (:value current-value-map) current-value-map)
         connection-method (get-in db [:resource-setup :roles role-index :connection-method] "manual-input")
         secrets-provider (get-in db [:resource-setup :roles role-index :secrets-manager-provider] "vault-kv1")
         default-source (if (= connection-method "secrets-manager")
                          secrets-provider
                          "manual-input")]
     (if (and (not (str/blank? current-key)) (not (str/blank? current-value)))
       (update-in db [:resource-setup :roles role-index]
                  #(-> %
                       (update :environment-variables (fnil conj [])
                               {:key current-key
                                :value current-value-map})
                       (merge {:env-current-key "" :env-current-value {:value "" :source default-source}})))
       db))))

(rf/reg-event-db
 :resource-setup->update-role-env-var
 (fn [db [_ role-index var-index field value]]
   (if (= field :value)
     (let [connection-method (get-in db [:resource-setup :roles role-index :connection-method] "manual-input")
           secrets-provider (get-in db [:resource-setup :roles role-index :secrets-manager-provider] "vault-kv1")
           existing-value (get-in db [:resource-setup :roles role-index :environment-variables var-index :value])
           existing-source (when (map? existing-value) (:source existing-value))
           inferred-source (or existing-source
                               (when (= connection-method "secrets-manager") secrets-provider)
                               "manual-input")
           new-value {:value (str value) :source inferred-source}]
       (assoc-in db [:resource-setup :roles role-index :environment-variables var-index field] new-value))
     (assoc-in db [:resource-setup :roles role-index :environment-variables var-index field] value))))

(rf/reg-event-db
 :resource-setup->update-role-env-var-source
 (fn [db [_ role-index var-index source]]
   (if (or (str/blank? source) (empty? source))
     db
     (let [is-secrets-provider? (contains? #{"vault-kv1" "vault-kv2" "aws-secrets-manager"} source)
           updated-db (update-in db [:resource-setup :roles role-index :environment-variables var-index :value]
                                 (fn [v]
                                   (let [value-str (if (map? v) (:value v) (str v))]
                                     {:value value-str :source source})))]
       (if (and is-secrets-provider?
                (not= (get-in updated-db [:resource-setup :roles role-index :secrets-manager-provider]) source))
         (update-in updated-db
                    [:resource-setup :roles role-index]
                    #(update-role-secrets-manager-provider % source))
         updated-db)))))

(rf/reg-event-db
 :resource-setup->update-role-env-current-value-source
 (fn [db [_ role-index source]]
   (if (or (str/blank? source) (empty? source))
     db
     (let [current-provider (get-in db [:resource-setup :roles role-index :secrets-manager-provider] "vault-kv1")
           current-value (get-in db [:resource-setup :roles role-index :env-current-value])
           current-source (when (map? current-value) (:source current-value))
           should-update? (not= current-source source)]
       (if should-update?
         (let [is-secrets-provider? (contains? #{"vault-kv1" "vault-kv2" "aws-secrets-manager"} source)
               updated-db (update-in db [:resource-setup :roles role-index :env-current-value]
                                     (fn [v]
                                       (let [value-str (if (map? v) (:value v) (str v))]
                                         {:value value-str :source source})))]
           (if (and is-secrets-provider?
                    (not= current-provider source))
             (update-in updated-db
                        [:resource-setup :roles role-index]
                        #(update-role-secrets-manager-provider % source))
             updated-db))
         db)))))

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
 (fn [db [_ role-index file-key value]]
   (let [config-files (get-in db [:resource-setup :roles role-index :configuration-files] [])
         existing-index (first (keep-indexed (fn [idx {:keys [key]}]
                                               (when (= key file-key) idx))
                                             config-files))]
     (if existing-index
       (assoc-in db [:resource-setup :roles role-index :configuration-files existing-index :value] value)
       (update-in db [:resource-setup :roles role-index :configuration-files]
                  (fnil conj [])
                  {:key file-key :value value})))))

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

