(ns webapp.audit.views.main
  (:require
   ["@radix-ui/themes" :refer [Box Flex Link Text]]
   [clojure.string :as string]
   [re-frame.core :as rf]
   [webapp.audit.views.audit-filters :as filters]
   [webapp.audit.views.sessions-list :as sessions-list]
   [webapp.components.loaders :as loaders]
   [webapp.config :as config]))

(defn- loading-list-view []
  [:div {:class "flex items-center justify-center rounded-none border bg-white h-full"}
   [:div {:class "flex items-center justify-center h-full"}
    [loaders/simple-loader]]])

(defn empty-list-view
  "Same shape as EmptyState in webapp_v2 (hoop-ui skill). With a filter set it
   is the compact no-match state; with none, the first-run state."
  [filtered?]
  [:> Box {:class (str "flex flex-col flex-1 items-center justify-center "
                       (if filtered? "py-8" "h-full"))}
   [:> Flex {:direction "column" :gap (if filtered? "4" "6") :align "center"
             :class "flex-1 justify-center"}
    [:> Box {:class "w-80"}
     [:img {:src "/images/illustrations/empty-state.png"
            :alt ""}]]

    [:> Box
     [:> Text {:as "p" :size "3" :weight "bold" :class "text-gray-11 text-center"}
      (if filtered? "No Sessions match your filters" "No Sessions yet")]
     [:> Text {:as "p" :size "2" :class "text-gray-11 text-center"}
      (if filtered?
        "Try clearing the filters above."
        "Connect to a Resource and your Sessions show here.")]]]

   (when-not filtered?
     [:> Flex {:align "center" :justify "center"}
      [:> Text {:size "2" :class "text-gray-11 mr-1"}
       "Need more information? Check out our"]
      [:> Link {:size "2"
                :href (get-in config/docs-url [:features :session-recording])
                :target "_blank"}
       "Sessions documentation"]
      [:> Text {:size "2" :class "text-gray-11"}
       "."]])])

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
                             (:status @sessions))
            filters (:filters @sessions)
            filtered? (or is-filtered-search-active?
                          (boolean (some (fn [[_ v]] (not (string/blank? (str v))))
                                         filters)))
            live-total (when (and (= (get filters "status") "open")
                                  (= (get filters "verb") "connect")
                                  (not is-filtered-search-active?))
                         (get-in @sessions [:sessions :total]))]
        ;; The filters and the list sit on the page, not in a card (hoop-ui
        ;; skill: no card inside a card). The list is square, like List in
        ;; webapp_v2.
        [:div {:class "flex flex-col h-full overflow-y-auto"}
         [:header
          [filters/audit-filters filters]]

         (when (and live-total (not= display-status :loading))
           [:> Text {:as "p" :size "2" :weight "medium" :class "text-gray-11 mb-3"
                     :role "status"}
            (str live-total (if (= live-total 1) " live session" " live sessions"))])

         (cond
           (and (empty? (:data display-sessions)) (not= display-status :loading))
           [empty-list-view filtered?]

           (and (= display-status :loading) (empty? (:data display-sessions)))
           [loading-list-view]

           :else
           [:div {:class "rounded-none border bg-white h-full overflow-y-auto"}
            [sessions-list/sessions-list
             display-sessions
             display-status]])]))))
