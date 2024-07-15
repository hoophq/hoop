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
  (let [sessions (rf/subscribe [:audit])]
    (rf/dispatch [:audit->get-sessions])
    (fn []
      [:div {:class "flex flex-col bg-white rounded-lg h-full p-6 overflow-y-auto"}
       [:header
        [filters/audit-filters
         (:filters @sessions)]]

       (if (and (= :loading (:status @sessions)) (empty? (:data @sessions)))
         [loading-list-view]

         [:div {:class "rounded-lg border bg-white h-full overflow-y-auto"}
          [sessions-list/sessions-list
           (:sessions @sessions)
           (:status @sessions)]])])))
