(ns webapp.connections.views.setup.events.effects
  (:require
   [re-frame.core :as rf]
   [webapp.connections.views.setup.events.process-form :as process-form]))

;; Initialize app state
(rf/reg-event-db
 :connection-setup/initialize-state
 (fn [db [_ initial-data]]
   (if initial-data
     (assoc db :connection-setup initial-data)
     (assoc db :connection-setup {}))))

;; Main effects that change multiple parts of state or interact with external systems
(rf/reg-event-fx
 :connection-setup/select-type
 (fn [{:keys [db]} [_ connection-type]]
   {:db (assoc-in db [:connection-setup :type] connection-type)
    :fx [[:dispatch [:connection-setup/next-step :credentials]]]}))

(rf/reg-event-fx
 :connection-setup/submit
 (fn [{:keys [db]} _]
   (let [current-env-key (get-in db [:connection-setup :credentials :current-key])
         current-env-value (get-in db [:connection-setup :credentials :current-value])
         current-file-name (get-in db [:connection-setup :credentials :current-file-name])
         current-file-content (get-in db [:connection-setup :credentials :current-file-content])

         db-with-current (cond-> db
                           (and (not (empty? current-env-key))
                                (not (empty? current-env-value)))
                           (update-in [:connection-setup :credentials :environment-variables]
                                      #(conj (or % []) {:key current-env-key :value current-env-value}))

                           (and (not (empty? current-file-name))
                                (not (empty? current-file-content)))
                           (update-in [:connection-setup :credentials :configuration-files]
                                      #(conj (or % []) {:key current-file-name :value current-file-content})))

         payload (process-form/process-payload db-with-current)]
     {:fx [[:dispatch [:connections->create-connection payload]]
           [:dispatch-later {:ms 500
                             :dispatch [:connection-setup/initialize-state nil]}]]})))
