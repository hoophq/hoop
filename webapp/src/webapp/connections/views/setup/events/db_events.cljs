(ns webapp.connections.views.setup.events.db-events
  (:require [re-frame.core :as rf]))

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
 :connection-setup/update-network-host
 (fn [db [_ value]]
   (assoc-in db [:connection-setup :network-credentials :host] value)))

(rf/reg-event-db
 :connection-setup/update-network-port
 (fn [db [_ value]]
   (assoc-in db [:connection-setup :network-credentials :port] value)))

;; Database specific events
(rf/reg-event-db
 :connection-setup/update-database-credentials
 (fn [db [_ field value]]
   (assoc-in db [:connection-setup :database-credentials field] value)))

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
                           (not current-value))]
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

;; Tags events
(rf/reg-event-db
 :connection-setup/set-tags
 (fn [db [_ tags]]
   (assoc-in db [:connection-setup :tags] tags)))

(rf/reg-event-db
 :connection-setup/set-tags-input
 (fn [db [_ value]]
   (assoc-in db [:connection-setup :tags-input] value)))

;; Review and Data Masking events
(rf/reg-event-db
 :connection-setup/set-review-groups
 (fn [db [_ groups]]
   (assoc-in db [:connection-setup :config :review-groups] groups)))

(rf/reg-event-db
 :connection-setup/set-data-masking-types
 (fn [db [_ types]]
   (assoc-in db [:connection-setup :config :data-masking-types] types)))

;; Environment Variables management
(rf/reg-event-db
 :connection-setup/add-env-row
 (fn [db [_]]
   (let [current-key (get-in db [:connection-setup :credentials :current-key])
         current-value (get-in db [:connection-setup :credentials :current-value])]
     (if (and (not (empty? current-key))
              (not (empty? current-value)))
       (-> db
           (update-in [:connection-setup :credentials :environment-variables]
                      #(conj (or % []) {:key current-key :value current-value}))
           (assoc-in [:connection-setup :credentials :current-key] "")
           (assoc-in [:connection-setup :credentials :current-value] ""))
       db))))

(rf/reg-event-db
 :connection-setup/update-env-current-key
 (fn [db [_ value]]
   (assoc-in db [:connection-setup :credentials :current-key] value)))

(rf/reg-event-db
 :connection-setup/update-env-current-value
 (fn [db [_ value]]
   (assoc-in db [:connection-setup :credentials :current-value] value)))

(rf/reg-event-db
 :connection-setup/update-env-var
 (fn [db [_ index field value]]
   (assoc-in db [:connection-setup :credentials :environment-variables index field] value)))

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

;; Navigation events
(rf/reg-event-db
 :connection-setup/next-step
 (fn [db [_ next-step]]
   (assoc-in db [:connection-setup :current-step] (or next-step :resource))))

(rf/reg-event-db
 :connection-setup/go-back
 (fn [db [_]]
   (let [current-step (get-in db [:connection-setup :current-step])]
     (case current-step
       :resource (.back js/history -1)
       :additional-config (assoc-in db [:connection-setup :current-step] :credentials)
       :credentials (-> db
                        (assoc-in [:connection-setup :current-step] :resource)
                        (assoc-in [:connection-setup :type] nil)
                        (assoc-in [:connection-setup :subtype] nil))
       :installation (-> db
                         (assoc-in [:connection-setup :current-step] :additional-config))
       (.back js/history -1)))))

(rf/reg-event-db
 :connection-setup/set-agent-id
 (fn [db [_ agent-id]]
   (assoc-in db [:connection-setup :agent-id] agent-id)))

;; Tags events
(rf/reg-event-db
 :connection-setup/add-tag
 (fn [db [_ key value]]
   (let [current-tags (get-in db [:connection-setup :tags] [])
         exists? (some #(= (:key %) key) current-tags)]
     (if exists?
       db
       (update-in db [:connection-setup :tags] conj {:key key :value value})))))

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
