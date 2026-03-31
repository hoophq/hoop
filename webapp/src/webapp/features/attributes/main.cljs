(ns webapp.features.attributes.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text]]
   [re-frame.core :as rf]
   [webapp.components.loaders :as loaders]
   [webapp.features.attributes.views.empty-state :as empty-state]
   [webapp.features.attributes.views.list :as attr-list]))

(defn main []
  (let [loading (rf/subscribe [:attributes/loading?])
        attributes (rf/subscribe [:attributes/list-data])]
    (rf/dispatch [:attributes/list])

    (fn []
      (let [loading? @loading
            list-data @attributes
            has-attributes? (seq list-data)]
        [:> Box {:class "bg-gray-1 p-radix-7 min-h-full h-max"}
         [:> Flex {:direction "column" :gap "6" :class "h-full"}
          [:> Flex {:justify "between" :align "center" :class "mb-6"}
           [:> Flex {:class "flex-col gap-2"}
            [:> Heading {:size "8" :weight "bold" :as "h1"} "Attributes"]
            [:> Text {:size "5" :class "text-[--gray-11]"}
             "Properties that control how features and security policies apply to connections. Assign attributes to connections to automatically enforce consistent behaviors."]]
           (when has-attributes?
             [:> Button {:size "3"
                         :onClick #(rf/dispatch [:navigate :settings-attributes-new])}
              "Create a new Attribute"])]

          (cond
            loading?
            [:> Box {:class "bg-gray-1 h-full"}
             [:> Flex {:direction "column" :justify "center" :align "center" :height "100%"}
              [loaders/simple-loader]]]

            (not has-attributes?)
            [empty-state/main]

            :else
            [attr-list/main])]]))))
