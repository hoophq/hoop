(ns webapp.ai-data-masking.connections-section
  (:require
   ["@radix-ui/themes" :refer [Box Flex Grid Heading Text]]
   [webapp.components.connections-select :as connections-select]))

(defn main [{:keys [connection-ids on-connections-change]}]
  (fn []
    [:> Grid {:columns "7" :gap "7"}
     [:> Box {:grid-column "span 2 / span 2"}
      [:> Flex {:align "center" :gap "2"}
       [:> Heading {:as "h3" :size "4" :weight "medium"} "Connection configuration"]]
      [:> Text {:size "3" :class "text-[--gray-11]"}
       "Select which connections to apply this configuration."]]

     [:> Box {:grid-column "span 5 / span 5"}
      [connections-select/main
       {:connection-ids @connection-ids
        :on-connections-change (fn [selected-options]
                                 (let [connection-ids (mapv #(:value %) selected-options)]
                                   (on-connections-change connection-ids)))}]]]))