(ns webapp.events.gateway-info
  (:require [re-frame.core :as rf]))

(rf/reg-event-fx
 :gateway->get-info
 (fn
   [{:keys [db]} [_]]
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
    :fx [[:dispatch [:tracking->initialize-if-allowed]]
         [:dispatch [:initialize-monitoring]]]}))

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

(rf/reg-sub
 :gateway->analytics-tracking
 (fn [db _]
   (= "enabled" (get-in db [:gateway->info :data :analytics_tracking] "disabled"))))

(rf/reg-sub
 :gateway->auth-method
 (fn [db _]
   (or
    (get-in db [:gateway->info :data :auth_method])
    "local")))

(rf/reg-sub
 :gateway->should-show-license-expiration-warning
 (fn [db _]
   (let [gateway-info (get-in db [:gateway->info :data])
         user-info (get-in db [:users->current-user :data])
         license-info (:license_info gateway-info)
         expire-at (:expire_at license-info)
         is-admin? (:admin? user-info)
         is-enterprise? (= (:type license-info) "enterprise")
         is-valid? (:is_valid license-info)]

     (when (and is-admin? is-enterprise? is-valid? expire-at)
       (let [now (js/Date.now)
             expire-date (* expire-at 1000) ; convert to milliseconds
             three-months-ms (* 90 24 60 60 1000) ; 90 days in milliseconds
             warning-date (- expire-date three-months-ms)]
         (>= now warning-date))))))
