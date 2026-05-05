(ns webapp.audit.views.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Link Popover
                               Text TextField]]
   ["lucide-react" :refer [ArrowRight Workflow]]
   [clojure.string :as string]
   [re-frame.core :as rf]
   [reagent.core :as r]
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

(defn- jump-to-workflow
  "Page-level utility (separated from filters) for navigating directly
   to a workflow timeline by correlation-id. Lives in the page header
   because it navigates rather than filters."
  []
  (let [open?  (r/atom false)
        value  (r/atom "")
        navigate (fn []
                   (let [trimmed (string/trim @value)]
                     (when-not (string/blank? trimmed)
                       (reset! open? false)
                       (reset! value "")
                       (rf/dispatch [:navigate :sessions-workflow
                                     {}
                                     :correlation-id
                                     (js/encodeURIComponent trimmed)]))))]
    (fn []
      [:> Popover.Root {:open @open?
                        :onOpenChange #(reset! open? %)}
       [:> Popover.Trigger {:asChild true}
        [:> Button {:size "2"
                    :variant "soft"
                    :color "gray"
                    :highContrast true
                    :class "h-[36px]"}
         [:> Workflow {:size 14}]
         [:> Text {:size "2" :weight "medium"} "Jump to workflow"]]]
       [:> Popover.Content {:size "2" :style {:width "360px"} :align "end"}
        [:> Flex {:direction "column" :gap "3"}
         [:> Flex {:direction "column" :gap "1"}
          [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
           "Open workflow timeline"]
          [:> Text {:size "1" :class "text-[--gray-11]"}
           "Paste a correlation ID to jump straight to its run."]]
         [:> TextField.Root
          {:size "2"
           :placeholder "e.g. order-sync-2026-05-05"
           :value @value
           :autoFocus true
           :onChange #(reset! value (-> % .-target .-value))
           :onKeyDown (fn [e]
                        (when (= (.-key e) "Enter")
                          (.preventDefault e)
                          (navigate)))}
          [:> TextField.Slot
           [:> Workflow {:size 14 :class "text-[--gray-10]"}]]]
         [:> Flex {:justify "end" :gap "2"}
          [:> Popover.Close
           [:> Button {:size "2" :variant "soft" :color "gray"}
            "Cancel"]]
          [:> Button {:size "2"
                      :variant "solid"
                      :color "gray"
                      :highContrast true
                      :disabled (string/blank? (string/trim @value))
                      :on-click navigate}
           [:> Text {:size "2" :weight "medium"} "Open"]
           [:> ArrowRight {:size 14}]]]]]])))

(defn- page-header
  "Slim subheader sitting above the filter row. The route layout already
   renders the page title; we only need a descriptive line and the
   page-level utilities (jump-to-workflow) that aren't filters."
  []
  [:> Flex {:align "center" :justify "between" :gap "4"
            :class "mb-radix-5"}
   [:> Text {:size "2" :class "text-[--gray-11] min-w-0"}
    "Browse, filter, and replay any session captured by the gateway."]
   [jump-to-workflow]])

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
          [page-header]
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
