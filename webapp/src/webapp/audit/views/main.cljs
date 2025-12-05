(ns webapp.audit.views.main
  (:require [re-frame.core :as rf]
            [webapp.audit.views.audit-filters :as filters]
            [webapp.audit.views.sessions-list :as sessions-list]
            [webapp.components.loaders :as loaders]))

(defn- loading-list-view []
  [:div {:class "flex items-center justify-center rounded-lg border bg-white h-full"}
   [:div {:class "flex items-center justify-center h-full"}
    [loaders/simple-loader]]])

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

         (if (and (= display-status :loading) (empty? (:data display-sessions)))
           [loading-list-view]
           [:div {:class "rounded-lg border bg-white h-full overflow-y-auto"}
            [sessions-list/sessions-list
             display-sessions
             display-status]])]))))
