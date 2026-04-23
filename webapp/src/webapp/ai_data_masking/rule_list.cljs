(ns webapp.ai-data-masking.rule-list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Text Badge]]
   ["lucide-react" :refer [ChevronDown ChevronUp]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.attribute-filter :as attribute-filter]
   [webapp.connections.constants :as connection-constants]
   [webapp.components.resource-role-filter :as resource-role-filter]
   [webapp.components.filtered-empty-state :refer [filtered-empty-state]]))

(defn- get-rule-connections
  [connections connection-ids]
  (filter #(some (fn [id] (= (:id %) id)) connection-ids) connections))

(defn- connections-panel [{:keys [connections connections-loading?]}]
  [:> Box {:px "7" :py "5" :class "border-t rounded-b-6 bg-white"}
   [:> Grid {:columns "7" :gap "7"}
    [:> Box {:grid-column "span 2 / span 2"}
     [:> Heading {:as "h4" :size "4" :weight "medium" :class "text-[--gray-12]"}
      "Connected Resources"]
     [:> Text {:size "3" :class "text-[--gray-11]"}
      "Resource Roles that are using this AI Data Masking rule."]]

    [:> Box {:class "h-fit border border-[--gray-a6] rounded-md" :grid-column "span 5 / span 5"}
     (if connections-loading?
       ;; Show loading state
       [:> Flex {:p "4" :align "center" :justify "center"}
        [:> Text {:size "2" :class "text-[--gray-11]"} "Loading connections..."]]

       ;; Show connections list
       (if (empty? connections)
         [:> Flex {:p "4" :align "center" :justify "center"}
          [:> Text {:size "2" :class "text-[--gray-11]"} "No connections found for this rule"]]

         (for [connection connections]
           ^{:key (:name connection)}
           [:> Flex {:p "2" :align "center" :justify "between" :class "last:border-b-0 border-b border-[--gray-a6]"}
            [:> Flex {:gap "2" :align "center"}
             [:> Box
              [:figure {:class "w-4"}
               [:img {:src  (connection-constants/get-connection-icon connection)
                      :class "w-9"}]]]
             [:span {:class "text-sm"} (:name connection)]]
            [:> Button {:size "1"
                        :variant "soft"
                        :color "gray"
                        :on-click (fn []
                                    (rf/dispatch [:connections->get-connection {:connection-name (:name connection)}])
                                    (rf/dispatch [:navigate :edit-connection {} :connection-name (:name connection)]))}
             "Configure"]])))]]])

(defn rule-item []
  (let [show-connections? (r/atom false)]
    (fn [{:keys [id name description supported_entity_types
                 custom_entity_types connections on-configure total-items connections-loading? connection_ids connections-results]}]
      [:> Box {:class (str "first:rounded-t-6 last:rounded-b-6 data-[state=open]:bg-[--accent-2] "
                           "border-[--gray-a6] border "
                           (when (> total-items 1) " first:border-b-0")
                           (when @show-connections? " bg-[--accent-2]"))}
       [:> Box {:p "5" :class "flex justify-between items-center"}
        [:> Flex {:direction "column" :gap "2"}
         [:> Flex {:align "center" :gap "2"}
          [:> Heading {:as "h3" :size "5" :weight "medium" :class "text-[--gray-12]"}
           name]]
         [:> Text {:size "3" :class "text-[--gray-11]"}
          (or description "No description")]
         [:> Flex {:gap "2" :wrap "wrap"}
          (concat
           ; Process supported entity types
           (mapcat (fn [entity-type]
                     (if (= (:name entity-type) "CUSTOM_SELECTION")
                       ; For CUSTOM_SELECTION, show individual entity_types as badges
                       (for [field-type (:entity_types entity-type)]
                         ^{:key field-type}
                         [:> Badge {:variant "soft" :size "1"}
                          field-type])
                       ; For other types, show the preset name
                       [^{:key (:name entity-type)}
                        [:> Badge {:variant "soft" :size "1"}
                         (:name entity-type)]]))
                   supported_entity_types)
           ; Process custom entity types
           (for [custom-type custom_entity_types]
             ^{:key (:name custom-type)}
             [:> Badge {:variant "soft" :size "1"}
              (:name custom-type)]))]]
        [:> Flex {:align "center" :gap "4"}
         [:> Button {:size "3"
                     :variant "soft"
                     :color "gray"
                     :on-click #(on-configure id)}
          "Configure"]
         ;; Always show connections button if rule has connection IDs
         (when (seq connection_ids)
           [:> Button {:size "1"
                       :variant "ghost"
                       :color "gray"
                       :on-click #(swap! show-connections? not)}
            "Resource Roles"
            (if @show-connections?
              [:> ChevronUp {:size 14}]
              [:> ChevronDown {:size 14}])])]]
       (when @show-connections?
         [connections-panel {:connections connections
                             :connections-loading? (or connections-loading? (nil? connections-results))}])])))

(defn main [{:keys [rules on-configure]}]
  (let [connections (rf/subscribe [:connections])
        selected-connection (r/atom nil)
        selected-attribute (r/atom nil)]
    (fn []
      (let [connections-data @connections
            connections-loading? (:loading connections-data)
            connections-results (:results connections-data)
            by-connection (if (nil? @selected-connection)
                            rules
                            (filter (fn [rule]
                                      (let [rule-connections (if (and connections-results (not connections-loading?))
                                                               (get-rule-connections
                                                                connections-results
                                                                (:connection_ids rule))
                                                               [])]
                                        (some #(= (:name %) @selected-connection) rule-connections)))
                                    rules))
            filtered-rules (if (nil? @selected-attribute)
                             by-connection
                             (filter #(some #{@selected-attribute} (or (:attributes %) []))
                                     by-connection))]
        [:<>
         [:> Flex {:mb "6" :gap "2"}
          [resource-role-filter/main {:selected @selected-connection
                                      :on-select #(reset! selected-connection %)
                                      :on-clear #(reset! selected-connection nil)
                                      :label "Resource Role"}]
          [attribute-filter/main {:selected @selected-attribute
                                  :on-select #(reset! selected-attribute %)
                                  :on-clear #(reset! selected-attribute nil)
                                  :label "Attribute"
                                  :placeholder "Search attributes"}]]

         [:> Box
          (if (empty? filtered-rules)
            [filtered-empty-state {:entity-name "AI Data Masking rule"
                                   :filter-value (cond
                                                   (and @selected-connection @selected-attribute)
                                                   (str @selected-connection ", " @selected-attribute)

                                                   @selected-connection
                                                   @selected-connection

                                                   @selected-attribute
                                                   @selected-attribute)}]
            (doall
             (for [rule filtered-rules]
               ^{:key (:id rule)}
               [rule-item
                (assoc rule
                       :total-items (count filtered-rules)
                       :on-configure on-configure
                       :connections-loading? connections-loading?
                       :connection_ids (:connection_ids rule)
                       :connections-results connections-results
                       :connections (if (and connections-results (not connections-loading?))
                                      (get-rule-connections
                                       connections-results
                                       (:connection_ids rule))
                                      []))])))]]))))
