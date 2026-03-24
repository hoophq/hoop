(ns webapp.connections.views.setup.attributes-inputs
  (:require
  ["@radix-ui/themes" :refer [Badge Box Flex Heading Text]]
   [re-frame.core :as rf]
   [webapp.components.multiselect :as multiselect]))

(defn main []
  (let [all-attributes (rf/subscribe [:attributes/list-data])
        connection-name (rf/subscribe [:connection-setup/connection-name])
        selected-names (rf/subscribe [:connection-setup/selected-attributes])
        initialized? (rf/subscribe [:connection-setup/attributes-initialized?])]
    (rf/dispatch [:attributes/list])

    (fn []
      (let [attributes-data @all-attributes
            current-connection @connection-name
            selected @selected-names
            initialized @initialized?]

        (when (and (not initialized) (seq attributes-data) current-connection)
          (let [initial (mapv :name (filter #(some #{current-connection} (:connection_names %)) attributes-data))]
            (rf/dispatch [:connection-setup/initialize-attributes initial])))

        [:> Box {:class "space-y-6"}
         [:> Box
          [:> Flex {:align "center" :gap "2"}
           [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
            "Attributes"]
           [:> Badge {:variant "solid" :color "green"}
            "NEW"]]
          [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
           "Properties that determine how access policies, guardrails, and other features apply to this resource role. Attributes are evaluated by rules you configure."]]

         [multiselect/creatable-select
          {:id "attributes-input"
           :name "attributes-input"
           :options (mapv #(hash-map :value (:name %) :label (:name %)) attributes-data)
           :default-value (mapv #(hash-map :value % :label %) selected)
           :placeholder "Select or type to create"
           :on-change (fn [selected-options]
                        (let [names (mapv :value (js->clj selected-options :keywordize-keys true))]
                          (rf/dispatch [:connection-setup/set-selected-attributes names])))
           :on-create-option (fn [input-value]
                               (rf/dispatch [:attributes/create-inline {:name input-value}])
                               (rf/dispatch [:connection-setup/set-selected-attributes
                                             (conj selected input-value)]))}]]))))
