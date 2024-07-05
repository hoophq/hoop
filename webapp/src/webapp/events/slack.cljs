(ns webapp.events.slack
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :slack->send-message->user
 (fn [{:keys [db]} [_ body]]
   (let [success (fn []
                   (rf/dispatch [:show-snackbar {:level :success
                                                 :text "Message sent to slack!"}]))]
     {:fx [[:dispatch [:fetch
                       {:method "POST"
                        :uri "/slack/reply"
                        :body body
                        :on-success success}]]]})))
