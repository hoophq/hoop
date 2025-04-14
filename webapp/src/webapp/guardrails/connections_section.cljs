(ns webapp.guardrails.connections-section
  (:require
   [re-frame.core :as rf]
   ["@radix-ui/themes" :refer [Box Heading Grid Flex]]
   [webapp.components.multiselect :as multiselect]))

(defn format-connections-for-select [connections]
  (mapv (fn [connection]
          {"value" (:id connection)
           "label" (:name connection)})
        connections))

(defn main
  "Render the connections selection component for guardrails

   Parameters:
   - connections-ids: atom containing array of connection IDs
   - on-connections-change: function to call when connections are changed"
  [{:keys [connection-ids on-connections-change]}]
  (let [connections-list (rf/subscribe [:guardrails->connections-list])]
    (fn []
      (let [connections-data (:data @connections-list)
            connections-options (format-connections-for-select connections-data)
            selected-values (mapv (fn [id]
                                    (first (filter #(= (get % "value") id) connections-options)))
                                  @connection-ids)]
        [:> Grid {:columns "7" :gap "7"}
         [:> Box {:grid-column "span 2 / span 2"}
          [:> Heading {:as "h3" :size "4" :weight "medium"} "Associate Connections"]
          [:p {:class "text-sm text-gray-500"}
           "Select the connections where this guardrail should be applied."]]

         [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
          [:> Flex {:direction "column" :gap "2"}
           [multiselect/main
            {:label "Connections"
             :options connections-options
             :default-value (if (empty? selected-values) nil selected-values)
             :on-change (fn [selected-options]
                          (let [new-connection-ids (mapv #(get % "value") (js->clj selected-options))]
                            (on-connections-change connection-ids new-connection-ids)))}]]]]))))
