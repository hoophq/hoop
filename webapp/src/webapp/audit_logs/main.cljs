(ns webapp.audit-logs.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text]]
   ["lucide-react" :refer [Download]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.audit-logs.events]
   [webapp.audit-logs.filters :as filters]
   [webapp.audit-logs.subs]
   [webapp.audit-logs.table :as table]
   [webapp.components.loaders :as loaders]))

(defn empty-state []
  [:> Box {:class "flex flex-col h-full items-center justify-center py-16 px-4 bg-white max-w-3xl mx-auto rounded-lg border border-gray-200"}
   [:> Flex {:direction "column" :align "center"}
    [:> Box {:class "mb-8 w-80"}
     [:img {:src "/images/illustrations/empty-state.png"
            :alt "Empty state illustration"}]]
    [:> Text {:size "3" :class "text-gray-11 max-w-md text-center"}
     "No Internal Audit Logs found matching your criteria"]]])

(defn main []
  (let [audit-logs-state (rf/subscribe [:audit-logs/data])
        min-loading-done (r/atom false)
        initial-load (r/atom true)]

    (rf/dispatch [:audit-logs/fetch {:page 1 :page-size 25}])

    (js/setTimeout #(reset! min-loading-done true) 1500)

    (fn []
      (let [loading? (or (= :loading (:status @audit-logs-state))
                         (not @min-loading-done))
            data (:data @audit-logs-state)
            pagination (:pagination @audit-logs-state)
            total (:total pagination 0)
            current-count (count data)
            show-empty? (and (not loading?)
                            (empty? data)
                            (not @initial-load))]

        (when (and (not loading?) @initial-load)
          (reset! initial-load false))

        (cond
          loading?
          [:> Box {:class "bg-gray-1 h-full"}
           [:> Flex {:direction "column" :justify "center" :align "center" :height "100%"}
            [loaders/simple-loader]]]

          show-empty?
          [:> Box {:class "min-h-screen bg-gray-1"}
           [:> Box {:class "p-radix-7"}
            [:> Flex {:justify "between" :align "center" :class "mb-radix-6"}
             [:> Heading {:as "h2" :size "8"} "Internal Audit Logs"]
             [:> Button {:size "3"
                         :variant "soft"
                         :color "gray"
                         :on-click #(rf/dispatch [:audit-logs/export])}
              [:> Download {:size 20}]
              "Export"]]

            [:> Box {:class "space-y-radix-5"}
             [:> Flex {:justify "between" :align "center"}
              [filters/main]]

             [empty-state]]]]

          :else
          [:> Box {:class "min-h-screen bg-gray-1"}
           [:> Box {:class "p-radix-7"}
            [:> Flex {:justify "between" :align "center" :class "mb-radix-6"}
             [:> Heading {:as "h2" :size "8"} "Internal Audit Logs"]
             [:> Button {:size "3"
                         :variant "soft"
                         :color "gray"
                         :on-click #(rf/dispatch [:audit-logs/export])}
              [:> Download {:size 20}]
              "Export"]]

            [:> Box {:class "space-y-radix-5"}
             [:> Flex {:justify "between" :align "center"}
              [:> Text {:size "3" :class "text-[--gray-11]"}
               (str "Showing " current-count " of " total " logs")]
              [filters/main]]

             [table/main]]]])))))
