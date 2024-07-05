(ns webapp.events.organization
  (:require [re-frame.core :as rf]))

(rf/reg-event-fx
 :organization->get-api-key
 (fn [{:keys [db]} [_ _]]
   {:fx [[:dispatch
          [:fetch {:method "GET"
                   :uri "/orgs/keys"
                   :on-success (fn [api-key]
                                 (rf/dispatch [:organization->set-api-key api-key]))}]]]
    :db (assoc db :organization->api-key {:loading true :data nil})}))


(rf/reg-event-fx
 :organization->set-api-key
 (fn
   [{:keys [db]} [_ api-key]]
   {:db (assoc db :organization->api-key {:loading false :data api-key})}))

(rf/reg-event-fx
 :organization->create-api-key
 (fn
   [{:keys [db]} [_ _]]
   {:fx [[:dispatch
          [:fetch {:method "POST"
                   :uri "/orgs/keys"
                   :on-success (fn [api-key]
                                 (rf/dispatch [:organization->set-api-key api-key]))}]]]}))
