(ns webapp.events.slack-plugin
  (:require [re-frame.core :as rf]))

(defn- encode-b64 [data]
  (try
    (js/btoa data)
    (catch js/Error _ (str ""))))

(rf/reg-event-fx
 :slack-plugin->slack-config
 (fn
   [{:keys [_]} [_ {:keys [slack-bot-token slack-app-token]}]]
   (let [payload {:SLACK_BOT_TOKEN (encode-b64 slack-bot-token)
                  :SLACK_APP_TOKEN (encode-b64 slack-app-token)}
         on-failure (fn [error]
                      (rf/dispatch [:show-snackbar {:text "Failed to configure Slack plugin" :level :error :details error}]))
         on-success (fn [_]
                      (rf/dispatch
                       [:show-snackbar {:level :success
                                        :text "Slack app configured!"}]))]
     {:fx [[:dispatch [:fetch {:method "PUT"
                               :uri (str "/plugins/slack/config")
                               :on-success on-success
                               :on-failure on-failure
                               :body payload}]]]})))
