(ns webapp.connections.views.setup.events.effects
  (:require
   [re-frame.core :as rf]
   [webapp.connections.views.setup.events.initial-state :as state]
   [webapp.connections.views.setup.events.process-form :as process-form]))

;; Initialize app state
(rf/reg-event-db
 :connection-setup/initialize-state
 (fn [db [_ initial-data]]
   (if initial-data
     ;; Se houver dados iniciais, merge com o estado inicial
     (assoc db :connection-setup (merge state/initial-state initial-data))
     ;; Se não, usa o estado inicial puro
     (assoc db :connection-setup state/initial-state))))

;; Main effects that change multiple parts of state or interact with external systems
(rf/reg-event-fx
 :connection-setup/select-type
 (fn [{:keys [db]} [_ connection-type]]
   {:db (assoc-in db [:connection-setup :type] connection-type)
    :fx [[:dispatch [:connection-setup/next-step :credentials]]]}))

(rf/reg-event-fx
 :connection-setup/submit
 (fn [{:keys [db]} _]
   (let [payload (process-form/process-payload db)]
     {:fx [[:dispatch [:connections->create-connection payload]]
           [:dispacth [:connection-setup/initialize-state nil]]]}
     #_{:db db})))

;; Eventos específicos para atualização
(rf/reg-event-fx
 :connection-setup/initialize-update
 (fn [{:keys [db]} [_ connection-data]]
   {:db (assoc db :connection-setup connection-data)}))

(rf/reg-event-fx
 :connection-setup/update-config
 (fn [{:keys [db]} [_ path value]]
   {:db (assoc-in db (concat [:connection-setup :config] path) value)}))
