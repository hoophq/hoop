(ns webapp.connections.views.setup.events.db-events
  (:require [re-frame.core :as rf]))

;; Basic db updates
(rf/reg-event-db
 :connection-setup/select-subtype
 (fn [db [_ subtype]]
   (js/console.log "Select subtype event - Subtype:" subtype)
   (assoc-in db [:connection-setup :subtype] subtype)))

;; App type and OS selection
(rf/reg-event-db
 :connection-setup/select-app-type
 (fn [db [_ app-type]]
   (-> db
       (assoc-in [:connection-setup :app-type] app-type)
       (assoc-in [:connection-setup :current-step] :os-type))))

(rf/reg-event-db
 :connection-setup/select-os-type
 (fn [db [_ os-type]]
   (-> db
       (assoc-in [:connection-setup :os-type] os-type)
       (assoc-in [:connection-setup :current-step] :additional-config))))

;; Environment variables and configuration
(rf/reg-event-db
 :connection-setup/add-environment-variable
 (fn [db [_]]
   (let [current-key (get-in db [:connection-setup :credentials :current-key])
         current-value (get-in db [:connection-setup :credentials :current-value])
         current-vars (get-in db [:connection-setup :credentials :environment-variables] [])]
     (-> db
         (update-in [:connection-setup :credentials :environment-variables]
                    #(conj (or % []) {:key current-key :value current-value}))
         (assoc-in [:connection-setup :credentials :current-key] "")
         (assoc-in [:connection-setup :credentials :current-value] "")))))

(rf/reg-event-db
 :connection-setup/add-configuration-file
 (fn [db [_ file]]
   (update-in db [:connection-setup :configuration-files]
              conj file)))

;; Database specific events
(rf/reg-event-db
 :connection-setup/select-database-type
 (fn [db [_ db-type]]
   (-> db
       (assoc-in [:connection-setup :database-type] db-type)
       (assoc-in [:connection-setup :current-step] :database-credentials))))

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
         ;; Quando review é desabilitado, limpa os grupos
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
   (update-in db [:connection-setup :config :database-schema] not)))

(rf/reg-event-db
 :connection-setup/toggle-access-mode
 (fn [db [_ mode]]
   (let [current-value (get-in db [:connection-setup :config :access-modes mode])
         ;; Se o valor atual for nil, considera como true (valor inicial)
         effective-value (if (nil? current-value) true current-value)]
     (assoc-in db [:connection-setup :config :access-modes mode] (not effective-value)))))

;; Basic form events
(rf/reg-event-db
 :connection-setup/set-name
 (fn [db [_ name]]
   (assoc-in db [:connection-setup :name] name)))

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

;; Navigation events
(rf/reg-event-db
 :connection-setup/next-step
 (fn [db [_ next-step]]
   (js/console.log "Next step event - Current:" (get-in db [:connection-setup :current-step])
                   "Next:" next-step)
   (assoc-in db [:connection-setup :current-step] (or next-step :resource))))

(rf/reg-event-db
 :connection-setup/go-back
 (fn [db [_]]
   (let [current-subtype (get-in db [:connection-setup :subtype])
         app-type (get-in db [:connection-setup :app-type])
         os-type (get-in db [:connection-setup :os-type])]
     (cond
       (and (= current-subtype "console") app-type os-type)
       (-> db
           (assoc-in [:connection-setup :os-type] nil)
           (assoc-in [:connection-setup :current-step] :os-type))

       os-type
       (-> db
           (assoc-in [:connection-setup :os-type] nil)
           (assoc-in [:connection-setup :current-step] :os-type))

       app-type
       (-> db
           (assoc-in [:connection-setup :app-type] nil)
           (assoc-in [:connection-setup :current-step] :app-type))

       current-subtype
       (-> db
           (assoc-in [:connection-setup :subtype] nil)
           (assoc-in [:connection-setup :current-step] :type))

       :else
       (assoc-in db [:connection-setup :type] nil)))))

(rf/reg-event-db
 :connection-setup/set-agent-id
 (fn [db [_ agent-id]]
   (assoc-in db [:connection-setup :agent-id] agent-id)))
