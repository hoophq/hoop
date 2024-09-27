(ns webapp.audit.views.sessions-filtered-by-id
  (:require [re-frame.core :as rf]
            [webapp.config :as config]
            [webapp.audit.views.session-item :as session-item]
            [webapp.components.loaders :as loaders]))

(defn empty-list-view []
  [:div {:class "pt-x-large"}
   [:figure
    {:class "w-1/6 mx-auto p-regular"}
    [:img {:src (str config/webapp-url "/images/illustrations/pc.svg")
           :class "w-full"}]]
   [:div {:class "px-large text-center"}
    [:div {:class "text-gray-700 text-sm font-bold"}
     "Beep boop, no sessions to look"]
    [:div {:class "text-gray-500 text-xs mb-large"}
     "There's nothing with this criteria"]]])

(defn loading-list-view []
  [:div {:class "flex items-center justify-center h-full"}
   [loaders/simple-loader]])

(defn main []
  (let [user (rf/subscribe [:users->current-user])
        session-list (rf/subscribe [:audit->filtered-session-by-id])]
    (rf/dispatch [:users->get-user])
    (fn []
      [:div {:class "px-large flex flex-col bg-white rounded-lg py-regular h-full"}
       (when (and (= (:status @session-list) :loading) (empty? (:data @session-list)))
         [loading-list-view])
       (when (and (empty? (:data @session-list)) (not= (:status @session-list) :loading))
         [empty-list-view])

       (when (= (:status @session-list) :ready)
         [:div {:class "relative border h-full rounded-lg overflow-auto"}
          (doall
           (for [session (:data @session-list)]
             ^{:key (:id session)}
             [:div
              [session-item/session-item session @user]]))])])))

