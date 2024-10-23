(ns webapp.events.hoop-app
  (:require
   [re-frame.core :as rf]
   [webapp.config :as config]))



(rf/reg-event-fx
 :hoop-app->get-my-configs
 (fn []
   (let [on-success #(rf/dispatch [::hoop-app->set-my-configs %])
         get-my-configs [:http-request
                         {:method "GET"
                          :options {:headers {:accept "application/json"
                                              "Content-Type" "application/json"}}
                          :url (str config/hoop-app-url "/configs")
                          :on-success on-success
                          :on-failure #(println :failure :hoop-app->get-my-configs %)}]]
     {:fx [[:dispatch get-my-configs]]})))

(rf/reg-event-fx
 ::hoop-app->set-my-configs
 (fn [{:keys [db]} [_ response]]
   {:db (assoc-in db [:hoop-app->my-configs] response)}))

(rf/reg-event-fx
 :hoop-app->update-my-configs
 (fn [_ [_ configs]]
   (let [on-success (fn []
                      (rf/dispatch [:hoop-app->get-my-configs]))
         on-failure (fn []
                      (rf/dispatch [:show-snackbar {:level :error
                                                    :text "Something went wrong. Please try again"}]))]
     {:fx [[:dispatch [:http-request
                       {:method "POST"
                        :options {:headers {:accept "application/json"
                                            "Content-Type" "application/json"}}
                        :url (str config/hoop-app-url "/configs")
                        :body configs
                        :on-success on-success
                        :on-failure on-failure}]]]})))

