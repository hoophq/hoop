(ns webapp.events.license
  (:require [re-frame.core :as rf]))

(rf/reg-event-fx
  :license->update-license-key
  (fn [_ [_ license-info]]
    (let [license-obj (.parse js/JSON license-info)]
    {:fx [[:dispatch
           [:fetch {:method "PUT"
                    :uri "/orgs/license"
                    :body license-obj
                    :on-success #(rf/dispatch [:license->set-new-license %])}]]]})))

(rf/reg-event-fx
  :license->set-new-license
  (fn [_ [_ license-response]]
    (println :license-response license-response)
    {:fx [[:dispatch [:gateway->get-info]]]}))
