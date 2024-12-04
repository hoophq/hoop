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
                    :on-success #(rf/dispatch [:gateway->get-info %])}]]]})))
