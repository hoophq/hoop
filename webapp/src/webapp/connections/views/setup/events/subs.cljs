(ns webapp.connections.views.setup.events.subs
  (:require
   [re-frame.core :as rf]
   [webapp.connections.views.setup.tags-utils :as tags-utils]))

(rf/reg-sub
 :connection-setup/connection-subtype
 (fn [db _]
   (get-in db [:connection-setup :subtype])))

(rf/reg-sub
 :connection-setup/metadata-credentials
 (fn [db _]
   (let [credentials (get-in db [:connection-setup :metadata-credentials] {})]
     (reduce-kv (fn [acc k v]
                  (assoc acc k (if (map? v) (:value v "") (str v))))
                {}
                credentials))))

(rf/reg-sub
 :connection-setup/connection-method
 (fn [db _]
   (get-in db [:connection-setup :connection-method] "manual-input")))

(rf/reg-sub
 :connection-setup/secrets-manager-provider
 (fn [db _]
   (get-in db [:connection-setup :secrets-manager-provider] "vault-kv1")))

(rf/reg-sub
 :connection-setup/field-source
 (fn [db [_  field-key]]
   (let [metadata-source (get-in db [:connection-setup :metadata-credentials field-key :source])
         credential-source (get-in db [:connection-setup :credentials field-key :source])
         ssh-source (get-in db [:connection-setup :ssh-credentials field-key :source])
         kubernetes-source (get-in db [:connection-setup :kubernetes-token (keyword field-key) :source])
         network-source (get-in db [:connection-setup :network-credentials (keyword field-key) :source])
         connection-method (get-in db [:connection-setup :connection-method] "manual-input")
         secrets-provider (get-in db [:connection-setup :secrets-manager-provider] "vault-kv1")
         default-source (if (= connection-method "secrets-manager")
                          secrets-provider
                          "manual-input")]
     (or metadata-source credential-source ssh-source kubernetes-source network-source default-source))))

(rf/reg-sub
 :connection-setup/command-args
 (fn [db]
   (get-in db [:connection-setup :command-args] [])))

(rf/reg-sub
 :connection-setup/command-current-arg
 (fn [db]
   (get-in db [:connection-setup :command-current-arg] "")))

;; Configuration and features
(rf/reg-sub
 :connection-setup/config
 (fn [db]
   (get-in db [:connection-setup :config])))

(rf/reg-sub
 :connection-setup/review
 :<- [:connection-setup/config]
 (fn [config]
   (:review config false)))

(rf/reg-sub
 :connection-setup/data-masking
 :<- [:connection-setup/config]
 (fn [config]
   (:data-masking config false)))

(rf/reg-sub
 :connection-setup/database-schema
 :<- [:connection-setup/config]
 (fn [config]
   (:database-schema config true)))

(rf/reg-sub
 :connection-setup/access-modes
 :<- [:connection-setup/config]
 (fn [config]
   (get-in config [:access-modes] {:runbooks true, :native true, :web true})))

;; Review and masking types
(rf/reg-sub
 :connection-setup/review-groups
 :<- [:connection-setup/config]
 (fn [config]
   (get-in config [:review-groups] [])))

(rf/reg-sub
 :connection-setup/min-review-approvals
 :<- [:connection-setup/config]
 (fn [config]
   (get-in config [:min-review-approvals])))

(rf/reg-sub
 :connection-setup/force-approve-groups
 :<- [:connection-setup/config]
 (fn [config]
   (get-in config [:force-approve-groups] [])))

(rf/reg-sub
 :connection-setup/data-masking-types
 :<- [:connection-setup/config]
 (fn [config]
   (get-in config [:data-masking-types] [])))

(rf/reg-sub
 :connection-setup/access-max-duration
 :<- [:connection-setup/config]
 (fn [config]
   (get-in config [:access-max-duration])))

(rf/reg-sub
 :connection-setup/network-credentials
 (fn [db]
   (let [credentials (get-in db [:connection-setup :network-credentials] {})]
     (reduce-kv (fn [acc k v]
                  (assoc acc k (if (map? v) (:value v "") v)))
                {}
                credentials))))

;; Agent
(rf/reg-sub
 :connection-setup/agent-id
 (fn [db]
   (get-in db [:connection-setup :agent-id])))

;; Jira template ID
(rf/reg-sub
 :connection-setup/jira-template-id
 (fn [db]
   (get-in db [:connection-setup :config :jira-template-id])))

;; Guardrails
(rf/reg-sub
 :connection-setup/guardrails
 (fn [db]
   (get-in db [:connection-setup :config :guardrails] [])))

;; Environment Variables
(rf/reg-sub
 :connection-setup/env-current-key
 (fn [db]
   (get-in db [:connection-setup :credentials :current-key])))

(rf/reg-sub
 :connection-setup/env-current-value
 (fn [db]
   (let [value (get-in db [:connection-setup :credentials :current-value] {:value "" :source "manual-input"})]
     (if (map? value) (:value value) value))))

(rf/reg-sub
 :connection-setup/env-current-value-source
 (fn [db]
   (let [value (get-in db [:connection-setup :credentials :current-value])
         connection-method (get-in db [:connection-setup :connection-method] "manual-input")
         secrets-provider (get-in db [:connection-setup :secrets-manager-provider] "vault-kv1")
         explicit-source (when (and value (map? value)) (:source value))
         default-source (if (= connection-method "secrets-manager")
                          secrets-provider
                          "manual-input")]
     (or explicit-source default-source))))

(rf/reg-sub
 :connection-setup/env-var-source
 (fn [db [_ var-index]]
   (let [env-vars (get-in db [:connection-setup :credentials :environment-variables] [])
         value (when (< var-index (count env-vars))
                 (get-in env-vars [var-index :value]))
         connection-method (get-in db [:connection-setup :connection-method] "manual-input")
         secrets-provider (get-in db [:connection-setup :secrets-manager-provider] "vault-kv1")
         explicit-source (when (and value (map? value)) (:source value))
         default-source (if (= connection-method "secrets-manager")
                          secrets-provider
                          "manual-input")]
     (or explicit-source default-source))))

(rf/reg-sub
 :connection-setup/environment-variables
 (fn [db]
   (let [env-vars (get-in db [:connection-setup :credentials :environment-variables] [])]
     (mapv (fn [{:keys [value] :as env-var}]
             (assoc env-var :value (if (map? value) (:value value) value)))
           env-vars))))

;; Configuration Files subscriptions
(rf/reg-sub
 :connection-setup/config-current-name
 (fn [db]
   (get-in db [:connection-setup :credentials :current-file-name])))

(rf/reg-sub
 :connection-setup/config-current-content
 (fn [db]
   (get-in db [:connection-setup :credentials :current-file-content])))

(rf/reg-sub
 :connection-setup/configuration-files
 (fn [db]
   (get-in db [:connection-setup :credentials :configuration-files] [])))

;; Tags subs
(rf/reg-sub
 :connection-setup/key-validation-error
 (fn [db]
   (get-in db [:connection-setup :tags :key-validation-error])))

(rf/reg-sub
 :connection-tags/data
 (fn [db]
   (get-in db [:connection-tags :data])))

(rf/reg-sub
 :connection-tags/loading?
 (fn [db]
   (get-in db [:connection-tags :loading?] true)))

(rf/reg-sub
 :connection-tags/key-options
 :<- [:connection-tags/data]
 (fn [tags-data]
   (when tags-data
     (:grouped-options (tags-utils/format-keys-for-select tags-data)))))

(rf/reg-sub
 :connection-setup/current-key
 (fn [db]
   (get-in db [:connection-setup :tags :current-key])))

(rf/reg-sub
 :connection-setup/current-value
 (fn [db]
   (get-in db [:connection-setup :tags :current-value])))

(rf/reg-sub
 :connection-setup/available-values
 (fn [db]
   (get-in db [:connection-setup :tags :available-values] [])))

(rf/reg-sub
 :connection-setup/tags
 (fn [db]
   (get-in db [:connection-setup :tags :data] [])))

(rf/reg-sub
 :connection-setup/available-values-for-index
 (fn [db [_ index]]
   (get-in db [:connection-setup :tags :available-values-for-index index] [])))

(rf/reg-sub
 :connection-setup/form-data
 (fn [db]
   (:connection-setup db)))

;; SSH subscriptions
(rf/reg-sub
 :connection-setup/ssh-credentials
 (fn [db]
   (let [credentials (get-in db [:connection-setup :ssh-credentials] {})]
     (reduce-kv (fn [acc k v]
                  (assoc acc k (if (map? v) (:value v "") v)))
                {}
                credentials))))

;; Resource Subtype Override subscription
(rf/reg-sub
 :connection-setup/resource-subtype-override
 (fn [db]
   (get-in db [:connection-setup :resource-subtype-override])))

;; Kubernetes Token subscription
(rf/reg-sub
 :connection-setup/kubernetes-token
 (fn [db]
   (let [token (get-in db [:connection-setup :kubernetes-token] {})]
     (reduce-kv (fn [acc k v]
                  (assoc acc k (if (map? v) (:value v "") v)))
                {}
                token))))
