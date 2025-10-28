(ns webapp.ai-data-masking.connections-section
  (:require
   ["@radix-ui/themes" :refer [Box Flex Grid Heading Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.connections-select :as connections-select]))

(defn main [{:keys [connection-ids on-connections-change]}]
  (let [ai-data-masking-active (rf/subscribe [:ai-data-masking->active-rule])
        prev-connection-ids (r/atom nil)]
    
    (fn []
      (let [current-connection-ids @connection-ids
            selected-connections (get-in @ai-data-masking-active [:data :connections] [])]

        ;; Fetch selected connections when connection-ids change
        (when (not= @prev-connection-ids current-connection-ids)
          (reset! prev-connection-ids current-connection-ids)
          (when (seq current-connection-ids)
            (rf/dispatch [:ai-data-masking/get-selected-connections current-connection-ids])))

        [:> Grid {:columns "7" :gap "7"}
         [:> Box {:grid-column "span 2 / span 2"}
          [:> Flex {:align "center" :gap "2"}
           [:> Heading {:as "h3" :size "4" :weight "medium"} "Connection configuration"]]
          [:> Text {:size "3" :class "text-[--gray-11]"}
           "Select which connections to apply this configuration."]]

         [:> Box {:grid-column "span 5 / span 5"}
          [connections-select/main
           {:connection-ids current-connection-ids
            :selected-connections selected-connections
            :on-connections-change (fn [selected-options]
                                     (let [new-connection-ids (mapv #(:value %) selected-options)]
                                       (on-connections-change new-connection-ids)))}]]]))))