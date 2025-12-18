(ns webapp.resources.setup.events.subs
  (:require
   [re-frame.core :as rf]))

;; Basic form state
(rf/reg-sub
 :resource-setup/current-step
 (fn [db _]
   (get-in db [:resource-setup :current-step])))

(rf/reg-sub
 :resource-setup/resource-type
 (fn [db _]
   (get-in db [:resource-setup :type])))

(rf/reg-sub
 :resource-setup/resource-subtype
 (fn [db _]
   (get-in db [:resource-setup :subtype])))

(rf/reg-sub
 :resource-setup/resource-name
 (fn [db _]
   (get-in db [:resource-setup :name])))

(rf/reg-sub
 :resource-setup/agent-id
 (fn [db _]
   (get-in db [:resource-setup :agent-id])))

;; Roles subscriptions
(rf/reg-sub
 :resource-setup/roles
 (fn [db _]
   (get-in db [:resource-setup :roles] [])))

(rf/reg-sub
 :resource-setup/role-credentials
 (fn [db [_ role-index]]
   (let [credentials (get-in db [:resource-setup :roles role-index :credentials] {})]
     (reduce-kv (fn [acc k v]
                  (assoc acc k (if (map? v) (:value v "") v)))
                {}
                credentials))))

(rf/reg-sub
 :resource-setup/role-connection-method
 (fn [db [_ role-index]]
   (get-in db [:resource-setup :roles role-index :connection-method] "manual-input")))

;; Metadata-driven fields for custom resources
(rf/reg-sub
 :resource-setup/metadata-credentials
 (fn [db [_ role-index]]
   (let [metadata-credentials (get-in db [:resource-setup :roles role-index :metadata-credentials] {})]
     (reduce-kv (fn [acc k v]
                  (assoc acc k (:value v "")))
                {}
                metadata-credentials))))

;; Environment variables and config files
(rf/reg-sub
 :resource-setup/role-env-vars
 (fn [db [_ role-index]]
   (get-in db [:resource-setup :roles role-index :environment-variables] [])))

(rf/reg-sub
 :resource-setup/role-config-files
 (fn [db [_ role-index]]
   (get-in db [:resource-setup :roles role-index :configuration-files] [])))

(rf/reg-sub
 :resource-setup/role-command-args
 (fn [db [_ role-index]]
   (get-in db [:resource-setup :roles role-index :command-args] [])))

(rf/reg-sub
 :resource-setup/role-command-current-arg
 (fn [db [_ role-index]]
   (get-in db [:resource-setup :roles role-index :command-current-arg] "")))

;; Created resource (for success screen)
(rf/reg-sub
 :resources->last-created
 (fn [db _]
   (get-in db [:resources :last-created])))

(rf/reg-sub
 :resources->creating?
 (fn [db _]
   (get-in db [:resources :creating?] false)))

;; From catalog
(rf/reg-sub
 :resource-setup/from-catalog?
 (fn [db _]
   (get-in db [:resource-setup :from-catalog?] false)))

(rf/reg-sub
 :resource-setup/agent-creation-mode
 (fn [db _]
   (get-in db [:resource-setup :agent-creation-mode] :select)))

;; Context (for add-role flow)
(rf/reg-sub
 :resource-setup/context
 (fn [db _]
   (get-in db [:resource-setup :context] :setup)))

(rf/reg-sub
 :resource-setup/resource-id
 (fn [db _]
   (get-in db [:resource-setup :resource-id])))

(rf/reg-sub
 :resource-setup/loading?
 (fn [db _]
   (get-in db [:resource-setup :loading?] false)))

(rf/reg-sub
 :resource-setup/submitting?
 (fn [db _]
   (get-in db [:resource-setup :submitting?] false)))

;; Last created roles (for add-role success screen)
(rf/reg-sub
 :resources->last-created-roles
 (fn [db _]
   (get-in db [:resources :last-created-roles] [])))

;; Processed roles (roles that were sent in the request)
(rf/reg-sub
 :resource-setup/processed-roles
 (fn [db _]
   (get-in db [:resource-setup :processed-roles] [])))

;; Environment variables - current fields (temporary)
(rf/reg-sub
 :resource-setup/role-env-current-key
 (fn [db [_ role-index]]
   (get-in db [:resource-setup :roles role-index :env-current-key] "")))

(rf/reg-sub
 :resource-setup/role-env-current-value
 (fn [db [_ role-index]]
   (get-in db [:resource-setup :roles role-index :env-current-value] "")))

;; Configuration files - current fields (temporary)
(rf/reg-sub
 :resource-setup/role-config-current-name
 (fn [db [_ role-index]]
   (get-in db [:resource-setup :roles role-index :config-current-name] "")))

(rf/reg-sub
 :resource-setup/role-config-current-content
 (fn [db [_ role-index]]
   (get-in db [:resource-setup :roles role-index :config-current-content] "")))

(rf/reg-sub
 :resource-setup/current-connection-metadata
 :<- [:connections->metadata]
 :<- [:resource-setup/resource-subtype]
 (fn [[metadata subtype] _]
   (when (and metadata subtype)
     (->> (:connections metadata)
          (filter #(= (get-in % [:resourceConfiguration :subtype]) subtype))
          first))))

;; Secrets Manager subscriptions
(rf/reg-sub
 :resource-setup/secrets-manager-provider
 (fn [db [_ role-index]]
   (get-in db [:resource-setup :roles role-index :secrets-manager-provider] "vault-kv1")))

(rf/reg-sub
:resource-setup/field-source
(fn [db [_ role-index field-key]]
  (let [metadata-source (get-in db [:resource-setup :roles role-index :metadata-credentials field-key :source])
        credential-source (get-in db [:resource-setup :roles role-index :credentials field-key :source])
        connection-method (get-in db [:resource-setup :roles role-index :connection-method] "manual-input")
        secrets-provider (get-in db [:resource-setup :roles role-index :secrets-manager-provider] "vault-kv1")
        default-source (if (= connection-method "secrets-manager")
                         secrets-provider
                         "manual-input")]
    (or metadata-source credential-source default-source))))
