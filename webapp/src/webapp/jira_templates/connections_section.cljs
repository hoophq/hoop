(ns webapp.jira-templates.connections-section
  (:require
   [re-frame.core :as rf]
   ["@radix-ui/themes" :refer [Box Heading Grid Flex]]
   [webapp.components.multiselect :as multiselect]))

(defn- format-connections-for-select [connections]
  (mapv (fn [connection]
          {"value" (:id connection)
           "label" (:name connection)})
        connections))

(defn main [props]
  (let [connections-atom (:connection-ids props)
        on-change (:on-connections-change props)
        connections-list (rf/subscribe [:jira-templates->connections-list])
        all-connections (:all-connections props)]
    (fn []
      (let [connections-data (:data @connections-list)
            connections-options (concat (format-connections-for-select connections-data)
                                        (format-connections-for-select all-connections))
            selected-values (mapv (fn [id]
                                    (first (filter #(= (get % "value") id) connections-options)))
                                  @connections-atom)]
        [:> Flex {:direction "column" :gap "5"}
         [:> Box
          [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
           "Associate Connections"]
          [:> Box {:class "text-sm text-gray-500 mb-2"}
           "Select connections where this template should be applied"]]

         [:> Box {:class "mb-5"}
          [multiselect/main
           {:label "Connections"
            :options connections-options
            :default-value (if (empty? selected-values) nil selected-values)
            :on-change (fn [selected-options]
                         (let [connection-ids (mapv #(get % "value") (js->clj selected-options))]
                           (on-change connection-ids)))}]]]))))
