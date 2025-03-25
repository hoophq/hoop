(ns webapp.events.ask-ai
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :ask-ai->set-config
 (fn [_ [_ status]]
   {:fx [[:dispatch [:fetch
                     {:method "PUT"
                      :uri "/orgs/features"
                      :body {:name "ask-ai",
                             :status status}
                      :on-success (fn [_]
                                    (rf/dispatch [:users->get-user])
                                    (rf/dispatch [:show-snackbar {:level :success
                                                                  :text "The Ask-AI configs were updated!"}]))}]]]}))

(rf/reg-event-fx
 :ask-ai->clear-ai-responses
 (fn
   [{:keys [db]} [_]]
   {:db (assoc db :ask-ai->question-responses [])}))
