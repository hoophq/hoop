(ns webapp.events.localauth
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
  :localauth->register
  (fn
    [{:keys [db]} [_ user]]
    {:fx [[:dispatch [:fetch
                      {:method "POST"
                       :uri "/localauth/register"
                       :body user
                       :on-success #(rf/dispatch [::localauth->set-token %])}]]]}))

(rf/reg-event-fx
  ::localauth->set-token
  (fn
    [{:keys [db]} [_ token]]
    (println "Setting token" token)
    {:fx [[]]}))

