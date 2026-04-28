(ns webapp.features.attributes.views.list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.filtered-empty-state :refer [filtered-empty-state]]
   [webapp.components.resource-role-filter :as resource-role-filter]))

(defn main []
  (let [attributes (rf/subscribe [:attributes/list-data])
        selected-connection (r/atom nil)
        handle-select (fn [conn-name]
                        (reset! selected-connection conn-name))
        handle-clear (fn []
                       (reset! selected-connection nil))]
    (fn []
      (let [attrs (or @attributes [])
            filtered-attrs (if @selected-connection
                             (filterv (fn [attr]
                                        (some #{@selected-connection}
                                              (or (:connection_names attr) [])))
                                      attrs)
                             attrs)]
        [:> Box {:class "w-full h-full space-y-radix-3"}
         [:> Flex {:pb "3"}
          [resource-role-filter/main {:selected @selected-connection
                                      :on-select handle-select
                                      :on-clear handle-clear}]]
         [:> Box {:class "min-h-full h-max"}
          (if (empty? filtered-attrs)
            [filtered-empty-state {:entity-name "attribute"
                                   :filter-value @selected-connection}]
            (doall
             (for [attr filtered-attrs]
               ^{:key (:name attr)}
               [:> Box {:class (str "first:rounded-t-lg border-x border-t "
                                    "last:rounded-b-lg bg-white last:border-b border-gray-200 "
                                    "p-[--space-5]")}
                [:> Flex {:justify "between" :align "start"}
                 [:> Box
                  [:> Text {:size "4" :weight "bold"}
                   (or (:name attr) "Unnamed Attribute")]
                  [:> Text {:as "p" :size "3" :class "text-[--gray-11]"}
                   (or (:description attr) "")]]
                 [:> Button {:variant "outline"
                             :color "gray"
                             :size "3"
                             :on-click #(rf/dispatch [:navigate :settings-attributes-edit {:name (:name attr)}])}
                  "Configure"]]])))]]))))
