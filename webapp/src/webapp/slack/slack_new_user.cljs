(ns webapp.slack.slack-new-user
  (:require [re-frame.core :as rf]
            [webapp.config :as config]))

(defn main [_]
  (let [user (rf/subscribe [:users->current-user])]
    (fn [slack-id]
      (let [data (:data @user)]
        (when (seq data)
          (rf/dispatch [:users->update-user-slack-id {:slack-id slack-id}]))

        [:div {:class "bg-white h-full pt-x-large"}
         [:figure
          {:class "w-1/3 mx-auto p-regular"}
          [:img {:src (str config/webapp-url "/images/illustrations/videogame.svg")
                 :class "w-full"}]]
         [:div {:class "px-large py-large text-center"}
          [:div {:class "text-gray-700 text-sm font-bold"}
           "You have registered successfully your user with slack hoop.dev app."]
          [:div {:class "text-gray-500 text-xs mb-large"}
           "Now enjoy your super powers on Slack 🚀"]]]))))
