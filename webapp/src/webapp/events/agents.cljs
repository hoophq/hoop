(ns webapp.events.agents
  (:require [re-frame.core :as rf]))

(rf/reg-event-fx
 ::get-agents
 (fn
   [{:keys [db]} [_]]
   {:fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/agents"
                      :on-success #(rf/dispatch [::set-agents %])}]]]}))

(rf/reg-event-fx
 :agents->get-agents
 (fn
   [{:keys [db]} [_]]
   {:db (assoc db :agents {:status :loading :data []})
    :fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/agents"
                      :on-success #(rf/dispatch [::set-agents %])}]]]}))
(rf/reg-event-fx
 ::set-agents
 (fn
   [{:keys [db]} [_ agents]]
   {:db (assoc db :agents {:status :ready :data agents})}))

(rf/reg-event-fx
 :agents->get-embedded-agents-connected
 (fn
   [{:keys [db]} [_]]
   {:fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/agents"
                      :on-success #(rf/dispatch [::agents->set-embedded-agents-connected
                                                 (filter (fn [agent]
                                                           (and (= "embedded" (:mode agent))
                                                                (= "CONNECTED" (:status agent)))) %)])}]]]}))
(rf/reg-event-fx
 ::agents->set-embedded-agents-connected
 (fn
   [{:keys [db]} [_ agents]]
   {:db (assoc db :agents-embedded agents)}))

