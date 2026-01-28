(ns webapp.connections.views.setup.events.db-events
  (:require
   [clojure.string :as str]
   [re-frame.core :as rf]
   [webapp.connections.views.setup.tags-utils :as tags-utils]
   [webapp.connections.views.setup.connection-method :as connection-method]))

;; Basic db updates
(rf/reg-event-fx
 :connection-setup/select-connection
 (fn [{:keys [db]} [_ type subtype]]
   {:db (-> db
            (assoc-in [:connection-setup :type] type)
            (assoc-in [:connection-setup :subtype] subtype))
    :fx [[:dispatch [:connection-setup/next-step :credentials]]]}))

;; App type and OS selection
(rf/reg-event-db
 :connection-setup/select-app-type
 (fn [db [_ app-type]]
   (-> db
       (assoc-in [:connection-setup :app-type] app-type))))

(rf/reg-event-db
 :connection-setup/select-os-type
 (fn [db [_ os-type]]
   (-> db
       (assoc-in [:connection-setup :os-type] os-type)
       (assoc-in [:connection-setup :current-step] :additional-config))))

;; Network specific events
(rf/reg-event-db
 :connection-setup/update-network-credentials
 (fn [db [_ field value]]
   (let [field-key (keyword field)
         current-value (get-in db [:connection-setup :network-credentials field-key])
         connection-method (get-in db [:connection-setup :connection-method] "manual-input")
         secrets-provider (get-in db [:connection-setup :secrets-manager-provider] "vault-kv1")
         existing-source (when (map? current-value) (:source current-value))
         inferred-source (or existing-source
                             (when (= connection-method "secrets-manager") secrets-provider)
                             "manual-input")
         new-value {:value (str value) :source inferred-source}]
     (assoc-in db [:connection-setup :network-credentials field-key] new-value))))

(rf/reg-event-db
 :connection-setup/toggle-network-insecure
 (fn [db [_ enabled?]]
   (assoc-in db [:connection-setup :network-credentials :insecure] (boolean enabled?))))

;; Database specific events
(rf/reg-event-db
 :connection-setup/update-database-credentials
 (fn [db [_ field value]]
   (assoc-in db [:connection-setup :database-credentials field] value)))

;; Metadata-driven specific events
(defn update-credentials-source
  "Helper function to update all credentials in a map to use the given source, preserving values."
  [credentials-map source]
  (update-vals (or credentials-map {})
               (fn [v]
                 (let [raw-value (if (map? v) (:value v) v)
                       normalized (connection-method/normalize-credential-value raw-value)]
                   {:value (:value normalized)
                    :source source}))))

(defn update-connection-metadata-credentials-source
  "Updates all metadata-credentials to use the given source, preserving values."
  [conn source]
  (update conn :metadata-credentials #(update-credentials-source % source)))

(defn update-connection-ssh-credentials-source
  "Updates all ssh-credentials to use the given source, preserving values."
  [conn source]
  (update conn :ssh-credentials #(update-credentials-source % source)))

(defn update-connection-kubernetes-token-source
  "Updates all kubernetes-token to use the given source, preserving values."
  [conn source]
  (update conn :kubernetes-token #(update-credentials-source % source)))

(defn update-connection-network-credentials-source
  "Updates all network-credentials to use the given source, preserving values."
  [conn source]
  (update conn :network-credentials #(update-credentials-source % source)))

(defn update-connection-secrets-manager-provider
  "Updates the secrets manager provider and all credentials sources."
  [conn provider]
  (let [secrets-providers #{"vault-kv1" "vault-kv2" "aws-secrets-manager"}
        connection-method (:connection-method conn "manual-input")
        is-secrets-manager? (= connection-method "secrets-manager")
        target-source (if is-secrets-manager? provider "manual-input")
        should-update-source? (fn [current-source]
                                (if is-secrets-manager?
                                  (or (nil? current-source)
                                      (= current-source "manual-input")
                                      (and (contains? secrets-providers current-source)
                                           (not= current-source provider)))
                                  (contains? secrets-providers current-source)))
        update-value-source (fn [value]
                             (let [current-source (when (map? value) (:source value))
                                   raw-value (if (map? value)
                                               (:value value)
                                               (str value))]
                               (if (should-update-source? current-source)
                                 (let [normalized (connection-method/normalize-credential-value raw-value)]
                                   {:value (:value normalized) :source target-source})
                                 value)))
        update-env-var-source (fn [env-var]
                                (let [value-map (:value env-var)]
                                  (assoc env-var :value (update-value-source value-map))))]
    (-> conn
        (assoc :secrets-manager-provider (if is-secrets-manager? provider (:secrets-manager-provider conn)))
        (update-connection-metadata-credentials-source target-source)
        (update-connection-ssh-credentials-source target-source)
        (update-connection-kubernetes-token-source target-source)
        (update-connection-network-credentials-source target-source)
        (update-in [:credentials :environment-variables]
                   (fn [env-vars]
                     (mapv update-env-var-source (or env-vars []))))
        (update-in [:credentials :current-value] update-value-source))))

(rf/reg-event-db
 :connection-setup/update-metadata-credentials
 (fn [db [_ field value]]
   (let [current-value (get-in db [:connection-setup :metadata-credentials field])
         connection-method (get-in db [:connection-setup :connection-method] "manual-input")
         secrets-provider (get-in db [:connection-setup :secrets-manager-provider] "vault-kv1")
         existing-source (when (map? current-value) (:source current-value))
         inferred-source (or existing-source
                             (when (= connection-method "secrets-manager") secrets-provider)
                             "manual-input")
         new-value {:value (str value) :source inferred-source}]
     (assoc-in db [:connection-setup :metadata-credentials field] new-value))))

(rf/reg-event-db
 :connection-setup/update-connection-method
 (fn [db [_ method]]
   (let [current-provider (get-in db [:connection-setup :secrets-manager-provider])
         provider (if (str/blank? current-provider) "vault-kv1" current-provider)]
     (update-in db [:connection-setup]
                (fn [conn]
                  (-> conn
                      (assoc :connection-method method)
                      (update-connection-secrets-manager-provider provider)))))))

(rf/reg-event-db
 :connection-setup/update-secrets-manager-provider
 (fn [db [_ provider]]
   (let [clean-provider (if (str/blank? provider) "vault-kv1" provider)]
     (update-in db [:connection-setup]
                update-connection-secrets-manager-provider
                clean-provider))))

(defn extract-credential-value [v]
  (if (map? v) (:value v) (or v "")))

(rf/reg-event-db
 :connection-setup/update-field-source
 (fn [db [_ field-key source]]
   (if (str/blank? source)
     db
     (let [is-secrets-provider? (contains? #{"vault-kv1" "vault-kv2" "aws-secrets-manager"} source)
           field-key-str (name field-key)
           field-key-kw (keyword field-key-str)

           credential-locations [{:path [:connection-setup :metadata-credentials]
                                  :key field-key}
                                 {:path [:connection-setup :ssh-credentials]
                                  :key field-key-str}
                                 {:path [:connection-setup :kubernetes-token]
                                  :key field-key-kw}
                                 {:path [:connection-setup :network-credentials]
                                  :key field-key-kw}]

           target-location (some (fn [{:keys [path key]}]
                                   (when (contains? (get-in db path {}) key)
                                     {:path path :key key}))
                                 credential-locations)

           updated-db (if target-location
                        (let [{:keys [path key]} target-location
                              existing-value (get-in db (conj path key))]
                          (assoc-in db (conj path key)
                                    {:value (extract-credential-value existing-value)
                                     :source source}))
                        (assoc-in db [:connection-setup :metadata-credentials field-key]
                                  {:value "" :source source}))]

       (if (and is-secrets-provider?
                (not= (get-in updated-db [:connection-setup :secrets-manager-provider]) source))
         (update-in updated-db [:connection-setup] #(update-connection-secrets-manager-provider % source))
         updated-db)))))



;; Configuration toggles
(rf/reg-event-db
 :connection-setup/toggle-review
 (fn [db [_]]
   (let [new-review-state (not (get-in db [:connection-setup :config :review]))]
     (-> db
         (assoc-in [:connection-setup :config :review] new-review-state)
         (assoc-in [:connection-setup :config :review-groups]
                   (when new-review-state
                     (get-in db [:connection-setup :config :review-groups])))))))

(rf/reg-event-db
 :connection-setup/toggle-data-masking
 (fn [db [_]]
   (update-in db [:connection-setup :config :data-masking] not)))

(rf/reg-event-db
 :connection-setup/toggle-database-schema
 (fn [db [_]]
   (let [current-value (get-in db [:connection-setup :config :database-schema])
         effective-value (if (nil? current-value)
                           true
                           current-value)]
     (assoc-in db [:connection-setup :config :database-schema] (not effective-value)))))

(rf/reg-event-db
 :connection-setup/toggle-access-mode
 (fn [db [_ mode]]
   (let [current-value (get-in db [:connection-setup :config :access-modes mode])
         effective-value (if (nil? current-value)
                           true
                           current-value)]
     (assoc-in db [:connection-setup :config :access-modes mode] (not effective-value)))))

;; Basic form events
(rf/reg-event-db
 :connection-setup/set-name
 (fn [db [_ name]]
   (assoc-in db [:connection-setup :name] name)))

(rf/reg-event-db
 :connection-setup/set-command
 (fn [db [_ command]]
   (assoc-in db [:connection-setup :command] command)))

(rf/reg-event-db
 :connection-setup/set-command-args
 (fn [db [_ args]]
   (assoc-in db [:connection-setup :command-args] args)))

(rf/reg-event-db
 :connection-setup/set-command-current-arg
 (fn [db [_ arg]]
   (assoc-in db [:connection-setup :command-current-arg] arg)))

;; Review and Data Masking events
(rf/reg-event-db
 :connection-setup/set-review-groups
 (fn [db [_ groups]]
   (assoc-in db [:connection-setup :config :review-groups] groups)))

(rf/reg-event-db
 :connection-setup/set-min-review-approvals
 (fn [db [_ value]]
   (assoc-in db [:connection-setup :config :min-review-approvals] value)))

(rf/reg-event-db
 :connection-setup/set-force-approve-groups
 (fn [db [_ groups]]
   (assoc-in db [:connection-setup :config :force-approve-groups] groups)))

(rf/reg-event-db
 :connection-setup/set-data-masking-types
 (fn [db [_ types]]
   (assoc-in db [:connection-setup :config :data-masking-types] types)))

(rf/reg-event-db
 :connection-setup/set-access-max-duration
 (fn [db [_ value]]
   (assoc-in db [:connection-setup :config :access-max-duration] value)))

;; Environment Variables management
(rf/reg-event-db
 :connection-setup/add-env-row
 (fn [db [_]]
   (let [current-key (get-in db [:connection-setup :credentials :current-key])
         current-value-map (get-in db [:connection-setup :credentials :current-value])
         current-value (if (map? current-value-map) (:value current-value-map) current-value-map)
         connection-method (get-in db [:connection-setup :connection-method] "manual-input")
         secrets-provider (get-in db [:connection-setup :secrets-manager-provider] "vault-kv1")
         default-source (if (= connection-method "secrets-manager")
                          secrets-provider
                          "manual-input")]
     (if (and (not (empty? current-key))
              (not (empty? current-value)))
       (-> db
           (update-in [:connection-setup :credentials :environment-variables]
                      (fn [value]
                        (if (seq value)
                          (conj value {:key current-key :value current-value-map})
                          [{:key current-key :value current-value-map}])))
           (assoc-in [:connection-setup :credentials :current-key] "")
           (assoc-in [:connection-setup :credentials :current-value] {:value "" :source default-source}))
       db))))

(rf/reg-event-db
 :connection-setup/update-env-current-key
 (fn [db [_ value]]
   (assoc-in db [:connection-setup :credentials :current-key] value)))

(rf/reg-event-db
 :connection-setup/update-env-current-value
 (fn [db [_ value]]
   (let [existing-value (get-in db [:connection-setup :credentials :current-value])
         existing-source (when (map? existing-value) (:source existing-value))
         connection-method (get-in db [:connection-setup :connection-method] "manual-input")
         secrets-provider (get-in db [:connection-setup :secrets-manager-provider] "vault-kv1")
         ;; Preserve existing source if it's a secrets provider, otherwise infer
         inferred-source (or existing-source
                             (when (= connection-method "secrets-manager") secrets-provider)
                             "manual-input")
         new-value {:value (str value) :source inferred-source}]
     (assoc-in db [:connection-setup :credentials :current-value] new-value))))

(rf/reg-event-db
 :connection-setup/update-env-var
 (fn [db [_ index field value]]
   (if (= field :value)
     (let [connection-method (get-in db [:connection-setup :connection-method] "manual-input")
           secrets-provider (get-in db [:connection-setup :secrets-manager-provider] "vault-kv1")
           existing-value (get-in db [:connection-setup :credentials :environment-variables index :value])
           existing-source (when (map? existing-value) (:source existing-value))
           inferred-source (or existing-source
                               (when (= connection-method "secrets-manager") secrets-provider)
                               "manual-input")
           new-value {:value (str value) :source inferred-source}]
       (assoc-in db [:connection-setup :credentials :environment-variables index field] new-value))
     (assoc-in db [:connection-setup :credentials :environment-variables index field] value))))

(defn update-env-value-source
  [db value-path source]
  (if (str/blank? source)
    db
    (let [is-secrets-provider? (contains? #{"vault-kv1" "vault-kv2" "aws-secrets-manager"} source)
          updated-db (update-in db value-path
                                (fn [v]
                                  (let [value-str (if (map? v) (:value v) (str v))]
                                    {:value value-str :source source})))]
      (if (and is-secrets-provider?
               (not= (get-in updated-db [:connection-setup :secrets-manager-provider]) source))
        (update-in updated-db [:connection-setup]
                   #(update-connection-secrets-manager-provider % source))
        updated-db))))

(rf/reg-event-db
 :connection-setup/update-env-var-source
 (fn [db [_ var-index source]]
   (update-env-value-source db
                            [:connection-setup :credentials :environment-variables var-index :value]
                            source)))

(rf/reg-event-db
 :connection-setup/update-env-current-value-source
 (fn [db [_ source]]
   (update-env-value-source db
                            [:connection-setup :credentials :current-value]
                            source)))

;; Resource Subtype Override events
(rf/reg-event-db
 :connection-setup/set-resource-subtype-override
 (fn [db [_ value]]
   (assoc-in db [:connection-setup :resource-subtype-override] value)))

;; Configuration Files events
(rf/reg-event-db
 :connection-setup/update-config-file-name
 (fn [db [_ value]]
   (assoc-in db [:connection-setup :credentials :current-file-name] value)))

(rf/reg-event-db
 :connection-setup/update-config-file-content
 (fn [db [_ value]]
   (assoc-in db [:connection-setup :credentials :current-file-content] value)))

(rf/reg-event-db
 :connection-setup/update-config-file
 (fn [db [_ index field value]]
   (assoc-in db [:connection-setup :credentials :configuration-files index field] value)))

(rf/reg-event-db
 :connection-setup/add-configuration-file
 (fn [db [_]]
   (let [current-name (get-in db [:connection-setup :credentials :current-file-name])
         current-content (get-in db [:connection-setup :credentials :current-file-content])]
     (if (and (not (empty? current-name))
              (not (empty? current-content)))
       (-> db
           (update-in [:connection-setup :credentials :configuration-files]
                      #(conj (or % []) {:key current-name :value current-content}))
           (assoc-in [:connection-setup :credentials :current-file-name] "")
           (assoc-in [:connection-setup :credentials :current-file-content] ""))
       db))))

(rf/reg-event-db
 :connection-setup/update-config-file-by-key
 (fn [db [_ file-key value]]
   (let [config-files (get-in db [:connection-setup :credentials :configuration-files] [])
         existing-index (first (keep-indexed (fn [idx {:keys [key]}]
                                               (when (= key file-key) idx))
                                             config-files))]
     (if existing-index
       (assoc-in db [:connection-setup :credentials :configuration-files existing-index :value] value)
       (update-in db [:connection-setup :credentials :configuration-files]
                  (fnil conj [])
                  {:key file-key :value (str value)})))))

;; Navigation events
(rf/reg-event-db
 :connection-setup/next-step
 (fn [db [_ next-step]]
   (assoc-in db [:connection-setup :current-step] (or next-step :resource))))

(rf/reg-event-fx
 :connection-setup/go-back
 (fn [{:keys [db]} [_]]
   (let [current-step (get-in db [:connection-setup :current-step])
         from-catalog? (get-in db [:connection-setup :from-catalog?])]
     (case current-step
       :resource (if from-catalog?
                   {:fx [[:dispatch [:navigate :resource-catalog]]]}
                   (do (.back js/history -1) {}))
       :additional-config {:db (assoc-in db [:connection-setup :current-step] :credentials)}
       :credentials (if from-catalog?
                      ;; Se veio do catálogo, verifica contexto para voltar ao lugar certo
                      (let [current-path (.. js/window -location -pathname)
                            is-onboarding? (str/includes? current-path "/onboarding")]
                        (if is-onboarding?
                          {:fx [[:dispatch [:navigate :onboarding-setup]]]}
                          {:fx [[:dispatch [:navigate :resource-catalog]]]}))
                      ;; Senão, limpa type/subtype e volta para resource
                      {:db (-> db
                               (assoc-in [:connection-setup :current-step] :resource)
                               (assoc-in [:connection-setup :type] nil)
                               (assoc-in [:connection-setup :subtype] nil))})
       :installation {:db (assoc-in db [:connection-setup :current-step] :additional-config)}
       ;; Default: volta uma página na história
       (do (.back js/history -1) {})))))

(rf/reg-event-db
 :connection-setup/set-agent-id
 (fn [db [_ agent-id]]
   (assoc-in db [:connection-setup :agent-id] agent-id)))

;; Tags events
(rf/reg-event-db
 :connection-tags/set
 (fn [db [_ tags-data]]
   (-> db
       (assoc-in [:connection-tags :data] tags-data)
       (assoc-in [:connection-tags :loading?] false))))

(rf/reg-event-db
 :connection-setup/set-key-validation-error
 (fn [db [_ error-message]]
   (assoc-in db [:connection-setup :tags :key-validation-error] error-message)))

(rf/reg-event-db
 :connection-setup/set-current-value
 (fn [db [_ current-value]]
   (assoc-in db [:connection-setup :tags :current-value] current-value)))

(rf/reg-event-db
 :connection-setup/clear-current-tag
 (fn [db _]
   (-> db
       (assoc-in [:connection-setup :tags :current-key] nil)
       (assoc-in [:connection-setup :tags :current-full-key] nil)
       (assoc-in [:connection-setup :tags :current-label] nil)
       (assoc-in [:connection-setup :tags :current-value] nil))))

(rf/reg-event-db
 :connection-setup/add-tag
 (fn [db [_ full-key value]]
   (let [label (tags-utils/extract-label full-key)]
     (if (and full-key
              (not (str/blank? full-key))
              value
              (not (str/blank? value)))
       (update-in db [:connection-setup :tags :data]
                  #(conj (or % []) {:key full-key
                                    :label label
                                    :value value}))
       db))))

(rf/reg-event-db
 :connection-setup/update-tag-value
 (fn [db [_ index selected-option]]
   (let [value (when selected-option (.-value selected-option))]
     (if (and value (not (str/blank? value)))
       (assoc-in db [:connection-setup :tags :data index :value] value)
       db))))

;; Guardrails events
(rf/reg-event-db
 :connection-setup/set-guardrails
 (fn [db [_ values]]
   (assoc-in db [:connection-setup :config :guardrails] values)))

;; Jira events
(rf/reg-event-db
 :connection-setup/set-jira-template-id
 (fn [db [_ jira-template-id]]
   (assoc-in db [:connection-setup :config :jira-template-id] jira-template-id)))

;; SSH specific events
(rf/reg-event-db
 :connection-setup/update-ssh-credentials
 (fn [db [_ field value]]
   (let [current-value (get-in db [:connection-setup :ssh-credentials field])
         connection-method (get-in db [:connection-setup :connection-method] "manual-input")
         secrets-provider (get-in db [:connection-setup :secrets-manager-provider] "vault-kv1")
         existing-source (when (map? current-value) (:source current-value))
         inferred-source (or existing-source
                             (when (= connection-method "secrets-manager") secrets-provider)
                             "manual-input")
         new-value {:value (str value) :source inferred-source}]
     (assoc-in db [:connection-setup :ssh-credentials field] new-value))))

(rf/reg-event-db
 :connection-setup/clear-ssh-credentials
 (fn [db _]
   (assoc-in db [:connection-setup :ssh-credentials] {})))

;; Kubernetes Token events
(rf/reg-event-db
 :connection-setup/set-kubernetes-token
 (fn [db [_ field value]]
   (let [field-key (keyword field)
         current-value (get-in db [:connection-setup :kubernetes-token field-key])
         connection-method (get-in db [:connection-setup :connection-method] "manual-input")
         secrets-provider (get-in db [:connection-setup :secrets-manager-provider] "vault-kv1")
         existing-source (when (map? current-value) (:source current-value))
         inferred-source (or existing-source
                             (when (= connection-method "secrets-manager") secrets-provider)
                             "manual-input")
         new-value {:value (str value) :source inferred-source}]
     (assoc-in db [:connection-setup :kubernetes-token field-key] new-value))))
