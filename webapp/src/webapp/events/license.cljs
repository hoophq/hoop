(ns webapp.events.license
  (:require [re-frame.core :as rf]))

(rf/reg-event-fx
 :license->update-license-key
 (fn [_ [_ license-info]]
   (try
     (let [license-obj (.parse js/JSON license-info)]
       {:fx [[:dispatch
              [:fetch {:method "PUT"
                       :uri "/orgs/license"
                       :body license-obj
                       :on-success (fn []
                                     (js/setTimeout
                                      #(rf/dispatch [:show-snackbar
                                                     {:level :success
                                                      :text "License updated successfully"}])
                                      500)
                                     (rf/dispatch [:gateway->get-info])
                                     (rf/dispatch [:users->get-user]))}]]]})
     (catch js/Error e
       {:fx [[:dispatch
              [:show-snackbar {:level :error
                               :text "Error processing license: invalid JSON format"
                               :details {:error (.-message e)}}]]]}))))
