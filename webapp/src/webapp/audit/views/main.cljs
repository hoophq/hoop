(ns webapp.audit.views.main
  (:require
   ["@radix-ui/themes" :refer [Box Flex Text Link]]
   [re-frame.core :as rf]
   [webapp.audit.views.audit-filters :as filters]
   [webapp.audit.views.sessions-list :as sessions-list]
   [webapp.components.loaders :as loaders]
   [webapp.config :as config]))

(defn- loading-list-view []
  [:div {:class "flex items-center justify-center rounded-lg border bg-white h-full"}
   [:div {:class "flex items-center justify-center h-full"}
    [loaders/simple-loader]]])

(defn empty-list-view []
  [:<>
   [:> Box {:class "flex flex-col flex-1 h-full items-center justify-center"}

    [:> Flex {:direction "column" :gap "6" :align "center"}
     [:> Box {:class "w-80"}
      [:img {:src "/images/illustrations/empty-state.png"
             :alt "Empty state illustration"}]]

     [:> Box
      [:> Text {:as "p" :size "3" :weight "bold" :class "text-gray-11 text-center"}
       "Nothing here yet with these filters."]
      [:> Text {:as "p" :size "2" :class "text-gray-11 text-center"}
       "Try changing them to explore more sessions."]]]]

   [:> Flex {:align "center" :justify "center"}
    [:> Text {:size "2" :class "text-gray-11 mr-1"}
     "Need more information? Check out"]
    [:> Link {:size "2"
              :href (get-in config/docs-url [:features :session-recording])
              :target "_blank"}
     "Sessions documentation"]
    [:> Text {:size "2" :class "text-gray-11"}
     "."]]])

(defn panel [_]
  (let [sessions (rf/subscribe [:audit])
        filtered-sessions (rf/subscribe [:audit->filtered-session-by-id])]
    (rf/dispatch [:audit->get-sessions])
    (fn []
      (let [is-filtered-search-active? (not= (:status @filtered-sessions) :idle)
            display-sessions (if is-filtered-search-active?
                               {:data (:data @filtered-sessions)
                                :has_next_page false}
                               (:sessions @sessions))
            display-status (if is-filtered-search-active?
                             (:status @filtered-sessions)
                             (:status @sessions))]
        [:div {:class "flex flex-col bg-white rounded-lg h-full p-6 overflow-y-auto"}
         [:header
          [filters/audit-filters
           (:filters @sessions)]]

         (cond
           (and (empty? (:data display-sessions)) (not= display-status :loading))
           [empty-list-view]

           (and (= display-status :loading) (empty? (:data display-sessions)))
           [loading-list-view]

           :else
           [:div {:class "rounded-lg border bg-white h-full overflow-y-auto"}
            [sessions-list/sessions-list
             display-sessions
             display-status]])]))))
