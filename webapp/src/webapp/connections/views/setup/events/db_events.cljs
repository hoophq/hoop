(ns webapp.connections.views.setup.events.db-events
  (:require [re-frame.core :as rf]))

;; Basic db updates
(rf/reg-event-db
 :connection-setup/select-subtype
 (fn [db [_ subtype]]
   (assoc-in db [:connection-setup :subtype] subtype)))

(rf/reg-event-db
 :connection-setup/update-step
 (fn [db [_ step]]
   (assoc-in db [:connection-setup :current-step] step)))

(rf/reg-event-db
 :connection-setup/update-credentials
 (fn [db [_ field value]]
   (assoc-in db [:connection-setup :credentials field] value)))

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
       (assoc-in [:connection-setup :current-step] :credentials))))

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
   (update-in db [:connection-setup :config :review] not)))

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
   (update-in db [:connection-setup :config :access-modes mode] not)))

;; Navigation and form state
(rf/reg-event-db
 :connection-setup/next-step
 (fn [db [_]]
   (let [current-step (get-in db [:connection-setup :current-step])]
     (assoc-in db [:connection-setup :current-step]
               (case current-step
                 :resource :additional-config
                 :additional-config :resource  ;; fallback
                 :resource)))))

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
