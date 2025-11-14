(ns webapp.jira-templates.connections-section
  (:require
   ["@radix-ui/themes" :refer [Box Heading Flex]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.connections-select :as connections-select]))

(defn main
  "Render the connections selection component for jira templates

   Parameters:
   - connection-ids: atom containing array of connection IDs
   - on-connections-change: function to call when connections are changed"
  [{:keys [connection-ids on-connections-change]}]
  (let [jira-templates-active (rf/subscribe [:jira-templates->active-template])
        connections-loaded (r/atom false)
        selected-connections-cache (r/atom {})] ; id -> {id, name}

    (fn []
      (let [current-connection-ids @connection-ids
            stored-connections (get-in @jira-templates-active [:data :connections] [])
            ;; Merge stored connections with cached ones
            all-selected-connections (vals (merge @selected-connections-cache
                                                  (into {} (map #(vector (:id %) %) stored-connections))))]

        ;; Only fetch selected connections on initial load when we have connection-ids
        ;; and haven't loaded them yet
        (when (and (seq current-connection-ids)
                   (not @connections-loaded)
                   (empty? stored-connections)
                   (not (get-in @jira-templates-active [:data :connections-load-state :loading])))
          (reset! connections-loaded true)
          (rf/dispatch [:jira-templates/get-selected-connections current-connection-ids]))

        ;; Reset the loaded flag when connection-ids become empty (new template)
        (when (and @connections-loaded (empty? current-connection-ids))
          (reset! connections-loaded false)
          (reset! selected-connections-cache {}))

        [:> Flex {:direction "column" :gap "5"}
         [:> Box
          [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
           "Associate Resource Roles"]
          [:> Box {:class "text-sm text-gray-500 mb-2"}
           "Select resource roles where this template should be applied"]]

         [:> Box {:class "mb-5"}
          [connections-select/main
           {:connection-ids current-connection-ids
            :selected-connections all-selected-connections
            :on-connections-change (fn [selected-options]
                                     ;; Update cache with selected connections
                                     (reset! selected-connections-cache
                                             (into {} (map #(vector (:value %) {:id (:value %) :name (:label %)}) selected-options)))
                                     (let [new-connection-ids (mapv #(:value %) selected-options)]
                                       (on-connections-change new-connection-ids)))}]]]))))
