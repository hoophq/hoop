(ns webapp.audit.views.sessions-list
  (:require [re-frame.core :as rf]
            [webapp.components.loaders :as loaders]
            [webapp.audit.views.session-item :as session-item]))

(defn empty-list-view []
  [:div {:class "pt-x-large"}
   [:figure
    {:class "w-1/6 mx-auto p-regular"}
    [:img {:src "/images/illustrations/pc.svg"
           :class "w-full"}]]
   [:div {:class "px-large text-center"}
    [:div {:class "text-gray-700 text-sm font-bold"}
     "Beep boop, no sessions to look"]
    [:div {:class "text-gray-500 text-xs mb-large"}
     "There's nothing with this criteria"]]])

(defn loading-list-view []
  [:div {:class "flex items-center justify-center h-full"}
   [loaders/simple-loader]])

(defn sessions-list []
  (let [user (rf/subscribe [:users->current-user])]
    (fn [sessions status]
      [:div {:class "relative h-full overflow-auto"}
       (when (and (= status :loading) (empty? (:data sessions)))
         [loading-list-view])
       (when (and (empty? (:data sessions)) (not= status :loading))
         [empty-list-view])
       (doall
        (for [session (:data sessions)]
          ^{:key (:id session)}
          [:div {:class (when (= status :loading) "opacity-50 pointer-events-none")}
           [session-item/session-item session @user]]))
       (when (:has_next_page sessions)
         [:div {:class "py-regular text-center"}
          [:a
           {:href "#"
            :class "text-sm text-blue-500"
            :on-click #(rf/dispatch [:audit->get-next-sessions-page])}
           "Load more sessions"]])])))

