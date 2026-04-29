(ns webapp.setup.events
  (:require [re-frame.core :as rf]))

(rf/reg-event-fx
 :setup->create-admin
 (fn [{:keys [db]} [_ user]]
   {:db (assoc db :setup->loading true :setup->error nil)
    :fx [[:dispatch [:fetch
                     {:method "POST"
                      :uri "/localauth/register"
                      :body user
                      :on-success #(rf/dispatch [::setup->on-success %1 %2])
                      :on-failure #(rf/dispatch [::setup->on-failure %])}]]]}))

(rf/reg-event-fx
 ::setup->on-success
 (fn [{:keys [db]} [_ _ headers]]
   (.setItem js/localStorage "jwt-token" (.get headers "Token"))
   {:db (assoc db :setup->loading false)
    :fx [[:dispatch [:gateway->get-public-info]]
         [:dispatch [:navigate :home]]]}))

(rf/reg-event-fx
 ::setup->on-failure
 (fn [{:keys [db]} [_ error]]
   {:db (assoc db :setup->loading false :setup->error error)}))

(rf/reg-sub
 :setup->loading
 (fn [db _]
   (:setup->loading db)))

(rf/reg-sub
 :setup->error
 (fn [db _]
   (:setup->error db)))
