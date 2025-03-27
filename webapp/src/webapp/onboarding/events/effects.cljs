(ns webapp.onboarding.events.effects
  (:require [re-frame.core :as rf]))

(rf/reg-event-fx
 :onboarding/check-user
 (fn [{:keys [db]} _]
   (let [user (get-in db [:users->current-user :data])
         connections (get-in db [:connections :results])]
     (cond
       ;; If not admin or has connections, redirect to home
       (or (not (:admin? user))
           (seq connections))
       {:fx [[:dispatch [:navigate :editor-plugin]]]}

       ;; Otherwise redirect to setup
       :else
       {:fx [[:dispatch [:navigate :onboarding-setup]]]}))))
