(ns webapp.events.gateway-info
  (:require [re-frame.core :as rf]))

(rf/reg-event-fx
 :gateway->get-info
 (fn
   [{:keys [db]} [_ _]]
   {:db (assoc db :gateway->info {:loading true
                                  :data (-> db :gateway->info :data)})
    :fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/serverinfo"
                      :on-success #(rf/dispatch [::gateway->set-info %])}]]]}))

(rf/reg-event-fx
 ::gateway->set-info
 (fn
   [{:keys [db]} [_ info]]
   {:db (assoc db :gateway->info {:loading false
                                  :data info})}))
