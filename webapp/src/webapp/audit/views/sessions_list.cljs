(ns webapp.audit.views.sessions-list
  (:require [re-frame.core :as rf]
            ["@radix-ui/themes" :refer [Box Flex Text]]
            [webapp.audit.views.session-item :as session-item]))

(defn sessions-list []
  (let [user (rf/subscribe [:users->current-user])]
    (fn [sessions]
      [:div {:class "relative h-full overflow-y-auto"}
       (doall
        (for [session (:data sessions)]
          ^{:key (:id session)}
          [session-item/session-item session @user]))
       (when (:has_next_page sessions)
         [:div {:class "py-regular text-center"}
          [:a
           {:href "#"
            :class "text-sm text-blue-500"
            :on-click #(rf/dispatch [:audit->get-next-sessions-page])}
           "Load more sessions"]])])))

