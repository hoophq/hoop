(ns webapp.audit-logs.main
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.audit-logs.events]
   [webapp.audit-logs.filters :as filters]
   [webapp.audit-logs.subs]
   [webapp.audit-logs.table :as table]
   [webapp.components.loaders :as loaders]))

(defn empty-state []
  [:> Box {:class "flex flex-col items-center justify-center py-16 px-4"}
   [:> Flex {:direction "column" :align "center"}
    [:> Box {:class "mb-8 w-80"}
     [:img {:src "/images/illustrations/empty-state.png"
            :alt "Empty state illustration"}]]
    [:> Text {:size "3" :class "text-gray-11 max-w-md text-center"}
     "No audit logs found"]]])

(defn main []
  (r/with-let [audit-logs-state (rf/subscribe [:audit-logs/data])
               min-loading-done (r/atom false)
               initial-load (r/atom true)
               _ (do
                   (rf/dispatch [:users->get-users])
                   (rf/dispatch [:audit-logs/fetch {:page 1 :page-size 25}])
                   (js/setTimeout #(reset! min-loading-done true) 1500))]

    (let [data (:data @audit-logs-state)
          status (:status @audit-logs-state)
          pagination (:pagination @audit-logs-state)
          total (:total pagination 0)
          current-count (count data)
          initial-loading? (and (= :loading status) (empty? data))
          show-empty? (and (not (= :loading status))
                           (empty? data)
                           (not @initial-load))]

      (when (and (not (= :loading status)) @initial-load (seq data))
        (reset! initial-load false))

      (cond
        (and initial-loading? (not @min-loading-done))
        [:> Box {:class "bg-gray-1 h-full"}
         [:> Flex {:direction "column" :justify "center" :align "center" :height "100%"}
          [loaders/simple-loader]]]

        show-empty?
        [:> Box {:class "min-h-screen bg-gray-1"}
         [:> Box {:class "px-radix-7 pb-radix-7"}
          [:> Box {:class "sticky top-0 z-10 bg-[--gray-1] pb-radix-5 -mx-radix-7 px-radix-7 pt-radix-7"}
           [:> Heading {:as "h2" :size "8" :class "mb-radix-6"} "Internal Audit Logs"]

           [:> Flex {:justify "between" :align "center"}
            [:> Text {:size "3" :class "text-[--gray-11]"}
             "Showing 0 of 0 logs"]
            [filters/main]]]

          [:> Box {:class "flex items-center justify-center mt-radix-5"}
           [empty-state]]]]

        :else
        [:> Box {:class "min-h-screen bg-gray-1"}
         [:> Box {:class "px-radix-7 pb-radix-7"}
          [:> Box {:class "sticky top-0 z-10 bg-[--gray-1] pb-radix-5 -mx-radix-7 px-radix-7 pt-radix-7"}
           [:> Heading {:as "h2" :size "8" :class "mb-radix-6"} "Internal Audit Logs"]

           [:> Flex {:justify "between" :align "center"}
            [:> Text {:size "3" :class "text-[--gray-11]"}
             (str "Showing " current-count " of " total " logs")]
            [filters/main]]]

          [:> Box {:class "mt-radix-5"}
           [table/main]]]]))

    (finally
      (rf/dispatch [:audit-logs/cleanup]))))
