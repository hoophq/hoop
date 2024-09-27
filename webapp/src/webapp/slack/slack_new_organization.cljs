(ns webapp.slack.slack-new-organization
  (:require [webapp.config :as config]))

(defn main []
  [:div {:class "bg-white h-full pt-x-large"}
   [:figure {:class "w-1/3 mx-auto p-regular"}
    [:img {:src (str config/webapp-url "/images/illustrations/keyboard.svg")
           :class "w-full"}]]
   [:div {:class "px-large py-large text-center"}
    [:div {:class "text-gray-700 text-sm font-bold"}
     "You have integrated successfully the hoop.dev app in your slack workspace."]]])

