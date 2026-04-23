(ns webapp.features.access-control.views.group-form
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.button :as button]
   [webapp.components.forms :as forms]
   [webapp.components.connections-select :as connections-select]
   [webapp.components.multiselect :as multiselect]))

(defn create-form []
  (let [group-name (r/atom "")
        description (r/atom "")
        selected-connections (r/atom [])
        selected-attributes (r/atom [])
        is-submitting (r/atom false)
        scroll-pos (r/atom 0)
        attributes-data (rf/subscribe [:attributes/list-data])]

    (rf/dispatch [:attributes/list])

    (fn []
      (let [all-attributes (or @attributes-data [])]
        [:> Box {:class "min-h-screen bg-gray-1"}
         [:form {:on-submit (fn [e]
                              (.preventDefault e)
                              (reset! is-submitting true)
                              (rf/dispatch [:access-control/create-group-with-permissions
                                            {:name @group-name
                                             :description @description
                                             :connections @selected-connections
                                             :attributes @selected-attributes}]))}

          [:<>
           [:> Flex {:p "5" :gap "2"}
            [button/HeaderBack]]
           [:> Box {:class (str "sticky top-0 z-50 bg-gray-1 px-7 py-7 "
                                (when (>= @scroll-pos 30)
                                  "border-b border-[--gray-a6]"))}
            [:> Flex {:justify "between"
                      :align "center"}
             [:> Heading {:as "h2" :size "8"}
              "Create new access control group"]
             [:> Flex {:gap "5" :align "center"}
              [:> Button {:size "3"
                          :loading @is-submitting
                          :disabled @is-submitting
                          :type "submit"}
               "Save"]]]]]

          [:> Box {:p "7" :class "space-y-radix-9"}
           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
              "Set group information"]
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "Used to identify your access control group."]]

            [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
             [forms/input
              {:placeholder "e.g. engineering-team"
               :label "Name"
               :value @group-name
               :required true
               :class "w-full"
               :autoFocus true
               :disabled @is-submitting
               :on-change #(reset! group-name (-> % .-target .-value))}]]]

           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
              "Resource Role configuration"]
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "Select which resource roles this group should have access to."]]

            [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
             [connections-select/main
              {:connection-ids (mapv :id @selected-connections)
               :selected-connections @selected-connections
               :on-connections-change (fn [new-connections]
                                        (let [connection-data (mapv (fn [conn]
                                                                      {:id (:value conn)
                                                                       :name (:label conn)})
                                                                    new-connections)]
                                          (reset! selected-connections connection-data)))}]]]

           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
              "Attribute configuration"]
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "Select which attributes this group should have access to."]]

            [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
             [multiselect/main
              {:label "Attributes"
               :id "group-attributes-input"
               :name "group-attributes-input"
               :options (mapv #(hash-map :value (:name %) :label (:name %)) all-attributes)
               :default-value (mapv #(hash-map :value % :label %) @selected-attributes)
               :placeholder "Select attributes..."
               :on-change (fn [selected-options]
                            (let [names (mapv :value (js->clj selected-options :keywordize-keys true))]
                              (reset! selected-attributes names)))}]]]]]]))))

(defn edit-form [group-id]
  (let [plugin-details (rf/subscribe [:plugins->plugin-details])
        group-connections (rf/subscribe [:access-control/group-permissions group-id])
        group-attributes (rf/subscribe [:access-control/group-attributes group-id])
        attributes-data (rf/subscribe [:attributes/list-data])
        selected-connections (r/atom nil)
        selected-attributes (r/atom nil)
        is-submitting (r/atom false)
        scroll-pos (r/atom 0)]

    (rf/dispatch [:plugins->get-plugin-by-name "access_control"])
    (rf/dispatch [:attributes/list])

    (fn [group-id]
      (when (and (nil? @selected-connections)
                 (not= (:status @plugin-details) :loading))
        (reset! selected-connections (vec (or @group-connections []))))

      (when (and (nil? @selected-attributes)
                 (seq @attributes-data))
        (reset! selected-attributes (vec (or @group-attributes []))))

      (let [plugin (:plugin @plugin-details)
            plugin-loaded? (and plugin (:name plugin) (= (:name plugin) "access_control"))
            all-attributes (or @attributes-data [])]

        [:> Box {:class "min-h-screen bg-gray-1"}
         [:form {:on-submit (fn [e]
                              (.preventDefault e)
                              (reset! is-submitting true)

                              (if plugin-loaded?
                                (do
                                  (rf/dispatch [:access-control/add-group-permissions
                                                {:group-id group-id
                                                 :connections (or @selected-connections [])
                                                 :plugin plugin}])
                                  (rf/dispatch [:access-control/update-group-attributes
                                                {:group-name group-id
                                                 :selected-attributes (or @selected-attributes [])}])
                                  (js/setTimeout #(rf/dispatch [:navigate :access-control]) 1000))

                                (do
                                  (rf/dispatch [:plugins->get-plugin-by-name "access_control"])
                                  (rf/dispatch [:show-snackbar {:level :error
                                                                :text "Failed to save group permissions"}])
                                  (js/setTimeout #(reset! is-submitting false) 1000))))}

          [:<>
           [:> Flex {:p "5" :gap "2"}
            [button/HeaderBack]]
           [:> Box {:class (str "sticky top-0 z-50 bg-gray-1 px-7 py-7 "
                                (when (>= @scroll-pos 30)
                                  "border-b border-[--gray-a6]"))}
            [:> Flex {:justify "between"
                      :align "center"}
             [:> Heading {:as "h2" :size "8"}
              (str "Edit group: " group-id)]
             [:> Flex {:gap "5" :align "center"}
              [:> Button {:size "4"
                          :variant "ghost"
                          :color "red"
                          :disabled @is-submitting
                          :type "button"
                          :on-click #(rf/dispatch [:dialog->open
                                                   {:title "Delete Group"
                                                    :text (str "Are you sure you want to delete the group '" group-id "'? This action cannot be undone.")
                                                    :text-action-button "Delete"
                                                    :action-button? true
                                                    :type :danger
                                                    :on-success (fn []
                                                                  (rf/dispatch [:access-control/delete-group group-id])
                                                                  (let [redirect-fn (fn [] (rf/dispatch [:navigate :access-control]))]
                                                                    (js/setTimeout redirect-fn 500)))}])}
               "Delete"]
              [:> Button {:size "3"
                          :loading @is-submitting
                          :disabled (or @is-submitting (not plugin-loaded?))
                          :type "submit"}
               "Save"]]]]]

          [:> Box {:p "7" :class "space-y-radix-9"}
           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
              "Set group information"]
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "Used to identify your access control group."]]

            [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
             [forms/input
              {:placeholder "e.g. engineering-team"
               :label "Name"
               :value group-id
               :disabled true
               :class "w-full"}]]]

           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
              "Resource Role configuration"]
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "Select which resource roles this group should have access to."]]

            [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
             [connections-select/main
              {:connection-ids (mapv :id (or @selected-connections []))
               :selected-connections (or @selected-connections [])
               :on-connections-change (fn [new-connections]
                                        (let [connection-data (mapv (fn [conn]
                                                                      {:id (:value conn)
                                                                       :name (:label conn)})
                                                                    new-connections)]
                                          (reset! selected-connections connection-data)))}]]]

           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
              "Attribute configuration"]
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "Select which attributes this group should have access to."]]

            [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
             [multiselect/main
              {:label "Attributes"
               :id "group-attributes-input"
               :name "group-attributes-input"
               :options (mapv #(hash-map :value (:name %) :label (:name %)) all-attributes)
               :default-value (mapv #(hash-map :value % :label %) (or @selected-attributes []))
               :placeholder "Select attributes..."
               :on-change (fn [selected-options]
                            (let [names (mapv :value (js->clj selected-options :keywordize-keys true))]
                              (reset! selected-attributes names)))}]]]]]]))))

(defn main [mode & [params]]
  (case mode
    :create [create-form]
    :edit [edit-form (:group-id params)]
    [:div "Invalid form mode"]))
