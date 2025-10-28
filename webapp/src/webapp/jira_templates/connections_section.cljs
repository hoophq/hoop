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
        prev-connection-ids (r/atom nil)]

    (fn []
      (let [current-connection-ids @connection-ids
            selected-connections (get-in @jira-templates-active [:data :connections] [])]

        ;; Fetch selected connections when connection-ids change
        (when (not= @prev-connection-ids current-connection-ids)
          (reset! prev-connection-ids current-connection-ids)
          (when (seq current-connection-ids)
            (rf/dispatch [:jira-templates/get-selected-connections current-connection-ids])))

        [:> Flex {:direction "column" :gap "5"}
         [:> Box
          [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
           "Associate Connections"]
          [:> Box {:class "text-sm text-gray-500 mb-2"}
           "Select connections where this template should be applied"]]

         [:> Box {:class "mb-5"}
          [connections-select/main
           {:connection-ids current-connection-ids
            :selected-connections selected-connections
            :on-connections-change (fn [selected-options]
                                     (let [new-connection-ids (mapv #(:value %) selected-options)]
                                       (on-connections-change new-connection-ids)))}]]]))))