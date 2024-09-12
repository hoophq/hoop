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
                       :on-success #(rf/dispatch [::localauth->set-token %1 %2])}]]]}))

(rf/reg-event-fx
  ::localauth->set-token
  (fn
    [{:keys [db]} [_ _ headers]]
    (.setItem js/sessionStorage "jwt-token" (.get headers "Token"))
    {:fx [[:dispatch [:navigate :home]]]}))

(rf/reg-event-fx
  :localauth->login
  (fn
    [{:keys [db]} [_ user]]
    {:fx [[:dispatch [:fetch
                      {:method "POST"
                       :uri "/localauth/login"
                       :body user
                       :on-success #(rf/dispatch [::localauth->set-token %1 %2])}]]]}))


