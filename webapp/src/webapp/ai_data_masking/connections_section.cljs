(ns webapp.ai-data-masking.connections-section
  (:require
   ["@radix-ui/themes" :refer [Box Flex Grid Heading Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.connections-select :as connections-select]))

(defn main [{:keys [connection-ids on-connections-change]}]
  (let [ai-data-masking-active (rf/subscribe [:ai-data-masking->active-rule])
        connections-loaded (r/atom false)
        selected-connections-cache (r/atom {})] ; id -> {id, name}

    (fn []
      (let [current-connection-ids @connection-ids
            stored-connections (get-in @ai-data-masking-active [:data :connections] [])
            ;; Merge stored connections with cached ones
            all-selected-connections (vals (merge @selected-connections-cache
                                                  (into {} (map #(vector (:id %) %) stored-connections))))]

        ;; Only fetch selected connections on initial load when we have connection-ids
        ;; and haven't loaded them yet
        (when (and (seq current-connection-ids)
                   (not @connections-loaded)
                   (empty? stored-connections)
                   (not (get-in @ai-data-masking-active [:data :connections-load-state :loading])))
          (reset! connections-loaded true)
          (rf/dispatch [:ai-data-masking/get-selected-connections current-connection-ids]))

        ;; Reset the loaded flag when connection-ids become empty
        (when (and @connections-loaded (empty? current-connection-ids))
          (reset! connections-loaded false)
          (reset! selected-connections-cache {}))

        [:> Grid {:columns "7" :gap "7"}
         [:> Box {:grid-column "span 2 / span 2"}
          [:> Flex {:align "center" :gap "2"}
           [:> Heading {:as "h3" :size "4" :weight "medium"} "Resource Role configuration"]]
          [:> Text {:size "3" :class "text-[--gray-11]"}
           "Select which resource roles to apply this configuration."]]

         [:> Box {:grid-column "span 5 / span 5"}
          [connections-select/main
           {:connection-ids current-connection-ids
            :selected-connections all-selected-connections
            :on-connections-change (fn [selected-options]
                                     ;; Update cache with selected connections
                                     (reset! selected-connections-cache
                                             (into {} (map #(vector (:value %) {:id (:value %) :name (:label %)}) selected-options)))
                                     (let [new-connection-ids (mapv #(:value %) selected-options)]
                                       (on-connections-change new-connection-ids)))}]]]))))
