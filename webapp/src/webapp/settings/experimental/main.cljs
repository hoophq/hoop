(ns webapp.settings.experimental.main
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Callout Flex Heading Switch Table Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]))

(def ^:private stability-color
  {"experimental" "orange"
   "beta" "blue"})

(defn- flag-row [{:keys [name description stability components enabled]}]
  (let [pending? @(rf/subscribe [:settings-experimental/pending? name])]
    [:> Table.Row
     [:> Table.Cell
      [:> Text {:size "2" :weight "medium" :class "text-[--gray-12]"} name]]
     [:> Table.Cell
      [:> Text {:size "2" :class "text-[--gray-11]"} description]]
     [:> Table.Cell
      [:> Badge {:variant "soft"
                 :size "1"
                 :color (get stability-color stability "gray")}
       stability]]
     [:> Table.Cell
      [:> Flex {:gap "1" :wrap "wrap"}
       (for [comp components]
         ^{:key comp}
         [:> Badge {:variant "outline" :size "1" :color "gray"} comp])]]
     [:> Table.Cell {:align "center"}
      [:> Flex {:align "center" :gap "2" :justify "center"}
       [:> Switch {:checked (boolean enabled)
                   :disabled pending?
                   :size "1"
                   :onCheckedChange #(rf/dispatch [:settings-experimental/toggle name %])}]
       [:> Text {:size "1" :weight "medium" :class "w-6"}
        (if enabled "On" "Off")]]]]))

(defn main []
  (let [min-loading-done (r/atom false)]

    (rf/dispatch [:settings-experimental/get-flags])
    (js/setTimeout #(reset! min-loading-done true) 1500)

    (fn []
      (let [status @(rf/subscribe [:settings-experimental/status])
            flags @(rf/subscribe [:settings-experimental/flags])
            loading? (or (= :loading status)
                         (not @min-loading-done))]

        (cond
          loading?
          [:> Box {:class "bg-gray-1 h-full"}
           [:> Flex {:direction "column" :justify "center" :align "center" :height "100%"}
            [loaders/simple-loader]]]

          (= :error status)
          [:> Box {:class "min-h-screen bg-gray-1"}
           [:> Box {:class "sticky top-0 z-50 bg-gray-1 p-radix-7"}
            [:> Heading {:as "h2" :size "8"} "Experimental features"]]
           [:> Box {:class "p-radix-7"}
            [:> Callout.Root {:color "red"}
             [:> Callout.Text
              [:> Flex {:align "center" :gap "3"}
               [:> Text {:size "3"} "Failed to load feature flags."]
               [:> Button {:size "1" :variant "soft" :color "red"
                           :on-click #(rf/dispatch [:settings-experimental/get-flags])}
                "Retry"]]]]]]

          (empty? flags)
          [:> Box {:class "min-h-screen bg-gray-1"}
           [:> Box {:class "sticky top-0 z-50 bg-gray-1 p-radix-7"}
            [:> Heading {:as "h2" :size "8"} "Experimental features"]]
           [:> Box {:class "p-radix-7"}
            [:> Callout.Root
             [:> Callout.Text
              "No experimental features are currently registered."]]]]

          :else
          [:> Box {:class "min-h-screen bg-gray-1"}
           [:> Box {:class "sticky top-0 z-50 bg-gray-1 p-radix-7"}
            [:> Heading {:as "h2" :size "8"} "Experimental features"]]

           [:> Box {:class "p-radix-7"}
            [:> Table.Root {:variant "surface" :size "2"}
             [:> Table.Header
              [:> Table.Row
               [:> Table.ColumnHeaderCell {:width "20%"} "Flag"]
               [:> Table.ColumnHeaderCell {:width "35%"} "Description"]
               [:> Table.ColumnHeaderCell {:width "12%"} "Stability"]
               [:> Table.ColumnHeaderCell {:width "20%"} "Components"]
               [:> Table.ColumnHeaderCell {:width "13%" :align "center"} "Enabled"]]]
             [:> Table.Body
              (doall
               (for [flag flags]
                 ^{:key (:name flag)}
                 [flag-row flag]))]]]])))))
