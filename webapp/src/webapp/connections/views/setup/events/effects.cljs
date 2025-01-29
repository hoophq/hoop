(ns webapp.connections.views.setup.events.effects
  (:require
   [re-frame.core :as rf]
   [webapp.connections.views.setup.events.initial-state :as initial-state]
   [webapp.connections.views.setup.events.process-form :as process-form]))

;; Initialize app state
(rf/reg-event-fx
 :connection-setup/initialize
 (fn [{:keys [db]} _]
   {:db (assoc db :connection-setup initial-state/initial-state)}))

;; Main effects that change multiple parts of state or interact with external systems
(rf/reg-event-fx
 :connection-setup/select-type
 (fn [{:keys [db]} [_ connection-type]]
   {:db (assoc-in db [:connection-setup :type] connection-type)
    :fx [[:dispatch [:connection-setup/update-step :credentials]]]}))

(rf/reg-event-fx
 :connection-setup/submit
 (fn [{:keys [db]} _]
   (let [payload (process-form/process-payload db)]
    ;;  {:fx [[:dispatch [:connections->create-connection payload]]]}
     {:db db})))
