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
                                  :data info})
    :fx [[:dispatch [:tracking->initialize-if-allowed]]]}))

(rf/reg-event-fx
 :gateway->get-public-info
 (fn
   [{:keys [db]} [_ _]]
   {:db (assoc db :gateway->public-info {:loading true
                                         :data (-> db :gateway->public-info :data)})
    :fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/publicserverinfo"
                      :on-success #(rf/dispatch [::gateway->set-public-info %])}]]]}))

(rf/reg-event-fx
 ::gateway->set-public-info
 (fn
   [{:keys [db]} [_ info]]
   {:db (assoc db :gateway->public-info {:loading false
                                         :data info})}))

;; Subscription for analytics_tracking
(rf/reg-sub
 :gateway->analytics-tracking
 (fn [db _]
   (= "enabled" (get-in db [:gateway->info :data :analytics_tracking] "disabled"))))
