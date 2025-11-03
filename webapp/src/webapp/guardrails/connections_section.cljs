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
        connections-loaded (r/atom false)
        selected-connections-cache (r/atom {})] ; id -> {id, name}

    (fn []
      (let [current-connection-ids @connection-ids
            stored-connections (get-in @guardrails-active [:data :connections] [])
            ;; Merge stored connections with cached ones
            all-selected-connections (vals (merge @selected-connections-cache
                                                  (into {} (map #(vector (:id %) %) stored-connections))))]

        ;; Only fetch selected connections on initial load when we have connection-ids
        ;; and haven't loaded them yet
        (when (and (seq current-connection-ids)
                   (not @connections-loaded)
                   (empty? stored-connections)
                   (not (get-in @guardrails-active [:data :connections-load-state :loading])))
          (reset! connections-loaded true)
          (rf/dispatch [:guardrails/get-selected-connections current-connection-ids]))

        ;; Reset the loaded flag when connection-ids become empty (new guardrail)
        (when (and @connections-loaded (empty? current-connection-ids))
          (reset! connections-loaded false)
          (reset! selected-connections-cache {}))

        [:> Grid {:columns "7" :gap "7"}
         [:> Box {:grid-column "span 2 / span 2"}
          [:> Heading {:as "h3" :size "4" :weight "medium"} "Associate Connections"]
          [:p {:class "text-sm text-gray-500"}
           "Select the connections where this guardrail should be applied."]]

         [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
          [:> Flex {:direction "column" :gap "2"}
           [connections-select/main
            {:connection-ids current-connection-ids
             :selected-connections all-selected-connections
             :on-connections-change (fn [selected-options]
                                      ;; Update cache with selected connections
                                      (reset! selected-connections-cache
                                              (into {} (map #(vector (:value %) {:id (:value %) :name (:label %)}) selected-options)))
                                      (let [new-connection-ids (mapv #(:value %) selected-options)]
                                        (on-connections-change connection-ids new-connection-ids)))}]]]]))))