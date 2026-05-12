(ns webapp.features.machine-identities.views.identity-list
  (:require
   ["@radix-ui/themes" :refer [Box Button DropdownMenu Flex Heading Text]]
   ["lucide-react" :refer [ArrowRight MoreVertical]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.attribute-filter :as attribute-filter]
   [webapp.components.filtered-empty-state :refer [filtered-empty-state]]
   [webapp.components.resource-role-filter :as resource-role-filter]
   [webapp.features.machine-identities.views.empty-state :as empty-state]))

(defn- identity-matches-connection? [identity connection-name]
  (let [names (or (:connection_names identity) [])]
    (boolean (some #(= % connection-name) names))))

(defn identity-item []
  (fn [{:keys [name description]}]
    [:> Box {:class "first:rounded-t-6 last:rounded-b-6 border-[--gray-a6] border-x border-t last:border-b bg-white"}
     [:> Box {:p "5" :class "flex justify-between items-center gap-4 min-h-[106px]"}
      [:> Flex {:direction "column" :gap "2" :class "flex-1 min-w-0"}
       [:> Heading {:as "h3" :size "5" :weight "medium" :class "text-[--gray-12]"}
        name]
       (when description
         [:> Text {:size "3" :class "text-[--gray-11]"}
          description])]

      [:> Flex {:align "center" :gap "3" :class "shrink-0"}
       [:> Button {:size "2"
                   :variant "soft"
                   :class "gap-1"
                   :on-click #(rf/dispatch [:navigate :machine-identities-roles {} :identity-name name])}
        [:> Text {:size "2" :weight "medium"}
         "View roles"]
        [:> ArrowRight {:size 16}]]

       [:> DropdownMenu.Root
        [:> DropdownMenu.Trigger
         [:> Button {:size "2"
                     :variant "ghost"
                     :color "gray"
                     :aria-label "More options"}
          [:> MoreVertical {:size 20}]]]
        [:> DropdownMenu.Content
         [:> DropdownMenu.Item
          {:on-click #(rf/dispatch [:navigate :machine-identities-edit {} :identity-name name])}
          "Configure"]
         [:> DropdownMenu.Item
          {:color "red"
           :on-click #(rf/dispatch [:dialog->open
                                    {:title "Delete Machine Identity"
                                     :text (str "Are you sure you want to delete '" name "'? This action cannot be undone.")
                                     :text-action-button "Delete"
                                     :action-button? true
                                     :type :danger
                                     :on-success (fn []
                                                   (rf/dispatch [:machine-identities/delete name]))}])}
          "Delete"]]]]]]))

(defn main []
  (let [identities (rf/subscribe [:machine-identities/identities])
        selected-connection (r/atom nil)
        selected-attribute (r/atom nil)]
    (fn []
      (let [all-identities (or @identities [])

            filtered-by-connection (if (nil? @selected-connection)
                                     all-identities
                                     (filter #(identity-matches-connection? % @selected-connection)
                                             all-identities))

            filtered-identities (if (nil? @selected-attribute)
                                  filtered-by-connection
                                  (filter #(some #{@selected-attribute} (:attributes %))
                                          filtered-by-connection))

            processed-identities (sort-by :name filtered-identities)]

        [:<>
         [:> Flex {:gap "2" :wrap "wrap" :mb "4"}
          [resource-role-filter/main {:selected @selected-connection
                                      :on-select #(reset! selected-connection %)
                                      :on-clear #(reset! selected-connection nil)
                                      :label "Resource Role"}]

          [attribute-filter/main {:selected @selected-attribute
                                  :on-select #(reset! selected-attribute %)
                                  :on-clear #(reset! selected-attribute nil)
                                  :label "Attributes"}]

          (when (or @selected-connection @selected-attribute)
            [:> Button {:size "2"
                        :variant "soft"
                        :color "gray"
                        :on-click (fn []
                                    (reset! selected-connection nil)
                                    (reset! selected-attribute nil))}
             "Clear Filters"])]

         [:> Box
          (if (empty? processed-identities)
            (if (or @selected-connection @selected-attribute)
              [filtered-empty-state {:entity-name "Machine Identity"
                                     :message "No machine identities match the applied filters"
                                     :subtitle "Try adjusting or clearing your filters to explore more identities."}]
              [empty-state/main])

            (doall
             (for [identity processed-identities]
               ^{:key (:name identity)}
               [identity-item identity])))]]))))
