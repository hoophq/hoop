(ns webapp.components.filtered-empty-state
  (:require
   ["@radix-ui/themes" :refer [Box Flex Text Link]]
   [clojure.string :as cs]))

(defn filtered-empty-state
  "Reusable empty state component for filtered lists.
   
   Props:
   - :entity-name (string) - Name of the entity being filtered (e.g., 'rule', 'template')
   - :entity-name-plural (string, optional) - Plural form (defaults to entity-name + 's')
   - :filter-name (string, optional) - Name of the filter (defaults to 'Resource Role')
   - :filter-value (string, optional) - Current filter value (connection name)
   - :docs-url (string, optional) - URL to documentation
   - :docs-label (string, optional) - Label for documentation link"
  [{:keys [entity-name entity-name-plural filter-name filter-value docs-url docs-label]
    :or {filter-name "Resource Role"}}]
  (let [plural-name (or entity-name-plural (str entity-name "s"))
        has-filter? (not (cs/blank? filter-value))]
    [:<>
     [:> Box {:class "flex flex-col flex-1 h-full items-center justify-center"}
      [:> Flex {:direction "column" :gap "6" :align "center"}
       [:> Box {:class "w-80"}
        [:img {:src "/images/illustrations/empty-state.png"
               :alt "Empty state illustration"}]]
       
       [:> Box
        [:> Text {:as "p" :size "3" :weight "bold" :class "text-gray-11 text-center"}
         (if has-filter?
           (str "No " plural-name " found for \"" filter-value "\"")
           (str "No " plural-name " found"))]
        (when has-filter?
          [:> Text {:as "p" :size "2" :class "text-gray-11 text-center"}
           (str "Try changing the " filter-name " filter to explore more " plural-name ".")])]]]
     
     (when (and docs-url docs-label)
       [:> Flex {:align "center" :justify "center" :mt "4"}
        [:> Text {:size "2" :class "text-gray-11 mr-1"}
         "Need more information? Check out"]
        [:> Link {:size "2" :href docs-url :target "_blank"}
         docs-label]
        [:> Text {:size "2" :class "text-gray-11"}
         "."]])]))
