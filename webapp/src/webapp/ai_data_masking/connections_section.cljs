(ns webapp.ai-data-masking.connections-section
  (:require
   ["@radix-ui/themes" :refer [Box Flex Grid Heading Text]]
   [re-frame.core :as rf]
   [webapp.components.multiselect :as multi-select]))

(defn main [{:keys [connection-ids on-connections-change]}]
  (let [connections (rf/subscribe [:connections])]
    (fn []
      (let [connection-options (mapv (fn [conn]
                                       {"value" (:id conn)
                                        "label" (:name conn)})
                                     (:results @connections))
            ; Convert connection IDs to objects for multiselect
            default-connections (mapv (fn [conn-id]
                                        (some #(when (= (get % "value") conn-id) %)
                                              connection-options))
                                      @connection-ids)
            ; Filter out nils in case some connection IDs don't exist
            filtered-defaults (filterv some? default-connections)]
        [:> Grid {:columns "7" :gap "7"}
         [:> Box {:grid-column "span 2 / span 2"}
          [:> Flex {:align "center" :gap "2"}
           [:> Heading {:as "h3" :size "4" :weight "medium"} "Connection configuration"]]
          [:> Text {:size "3" :class "text-[--gray-11]"}
           "Select which connections to apply this configuration."]]

         [:> Box {:grid-column "span 5 / span 5"}
          [multi-select/main
           {:label "Connections"
            :name "connections"
            :default-value filtered-defaults
            :on-change on-connections-change
            :placeholder "Select one or more connections"
            :options connection-options}]]]))))
