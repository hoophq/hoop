(ns webapp.features.machine-identities.views.identity-list
  (:require
   ["@radix-ui/themes" :refer [Box Button DropdownMenu Flex Heading Text]]
   ["lucide-react" :refer [ArrowRight MoreVertical]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.filtered-empty-state :refer [filtered-empty-state]]
   [webapp.components.resource-role-filter :as resource-role-filter]
   [webapp.features.machine-identities.views.empty-state :as empty-state]))

(defn- identity-matches-connection? [identity connection-name]
  (let [names (or (:connection-names identity)
                  (when-let [n (:connection-name identity)]
                    [n]))]
    (boolean (some #(= % connection-name) names))))

(defn identity-item []
  (fn [{:keys [id name description]}]
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
                   :on-click #(rf/dispatch [:navigate :machine-identities-roles {} :identity-id id])}
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
         [:> DropdownMenu.Item {:on-click #(rf/dispatch [:navigate :machine-identities-edit {} :identity-id id])}
          "Configure"]
         [:> DropdownMenu.Separator]
         [:> DropdownMenu.Item {:color "red"
                                :on-click #(when (js/confirm (str "Are you sure you want to delete '" name "'?"))
                                             (rf/dispatch [:machine-identities/delete id]))}
          "Delete"]]]]]]))

(defn main []
  (let [identities (rf/subscribe [:machine-identities/identities])
        selected-connection (r/atom nil)
        selected-attributes (r/atom #{})]
    (fn []
      (let [all-identities (or @identities [])

            filtered-by-connection (if (nil? @selected-connection)
                                     all-identities
                                     (filter #(identity-matches-connection? % @selected-connection)
                                             all-identities))

            filtered-identities (if (empty? @selected-attributes)
                                  filtered-by-connection
                                  (filter #(some @selected-attributes (:attributes %))
                                          filtered-by-connection))

            processed-identities (sort-by :name filtered-identities)

            all-attributes (->> all-identities
                                (mapcat :attributes)
                                (distinct)
                                (sort))]

        [:<>
         [:> Flex {:gap "2" :mb "6" :wrap "wrap"}
          [resource-role-filter/main {:selected @selected-connection
                                      :on-select #(reset! selected-connection %)
                                      :on-clear #(reset! selected-connection nil)
                                      :label "Resource Role"}]

          (when (seq all-attributes)
            [:> DropdownMenu.Root
             [:> DropdownMenu.Trigger
              [:> Button {:size "2"
                          :variant (if (seq @selected-attributes) "soft" "surface")
                          :color "gray"
                          :class "gap-2"}
               [:> Text {:size "2" :weight "medium"}
                (if (seq @selected-attributes)
                  (str "Attributes (" (count @selected-attributes) ")")
                  "Attributes")]]]
             [:> DropdownMenu.Content {:style {:max-height "300px" :overflow-y "auto"}}
              (for [attr all-attributes]
                ^{:key attr}
                [:> DropdownMenu.CheckboxItem
                 {:checked (contains? @selected-attributes attr)
                  :on-checked-change #(if %
                                        (swap! selected-attributes conj attr)
                                        (swap! selected-attributes disj attr))}
                 attr])]])

          (when (or @selected-connection (seq @selected-attributes))
            [:> Button {:size "2"
                        :variant "soft"
                        :color "gray"
                        :on-click (fn []
                                    (reset! selected-connection nil)
                                    (reset! selected-attributes #{}))}
             "Clear Filters"])]

         [:> Box
          (if (empty? processed-identities)
            (if (or @selected-connection (seq @selected-attributes))
              [filtered-empty-state {:entity-name "Machine Identity"
                                     :filter-value (or @selected-connection
                                                       (when (seq @selected-attributes)
                                                         (str (count @selected-attributes) " attributes")))}]
              [empty-state/main])

            (doall
             (for [identity processed-identities]
               ^{:key (:id identity)}
               [identity-item identity])))]]))))
