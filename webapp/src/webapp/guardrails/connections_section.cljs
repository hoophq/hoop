(ns webapp.guardrails.connections-section
  (:require
   ["@radix-ui/themes" :refer [Box Heading Grid Flex]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.connections-select :as connections-select]))

(defn main
  "Render the connections selection component for guardrails

   Parameters:
   - connection-ids: atom containing array of connection IDs
   - on-connections-change: function to call when connections are changed"
  [{:keys [connection-ids on-connections-change]}]
  (let [guardrails-active (rf/subscribe [:guardrails->active-guardrail])
        prev-connection-ids (r/atom nil)]

    (fn []
      (let [current-connection-ids @connection-ids
            selected-connections (get-in @guardrails-active [:data :connections] [])]

        ;; Fetch selected connections when connection-ids change
        (when (not= @prev-connection-ids current-connection-ids)
          (reset! prev-connection-ids current-connection-ids)
          (when (seq current-connection-ids)
            (rf/dispatch [:guardrails/get-selected-connections current-connection-ids])))

        [:> Grid {:columns "7" :gap "7"}
         [:> Box {:grid-column "span 2 / span 2"}
          [:> Heading {:as "h3" :size "4" :weight "medium"} "Associate Connections"]
          [:p {:class "text-sm text-gray-500"}
           "Select the connections where this guardrail should be applied."]]

         [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
          [:> Flex {:direction "column" :gap "2"}
           [connections-select/main
            {:connection-ids current-connection-ids
             :selected-connections selected-connections
             :on-connections-change (fn [selected-options]
                                      (let [new-connection-ids (mapv #(:value %) selected-options)]
                                        (on-connections-change connection-ids new-connection-ids)))}]]]]))))