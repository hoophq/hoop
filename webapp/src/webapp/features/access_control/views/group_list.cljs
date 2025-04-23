(ns webapp.features.access-control.views.group-list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Card Text Badge IconButton Separator]]
   ["@heroicons/react/24/outline" :as hero-outline]
   ["@radix-ui/react-dropdown-menu" :as dropdown-menu]
   [re-frame.core :as rf]
   [webapp.components.headings :as h]
   [webapp.features.access-control.subs :as subs]))

(defn group-item [{:keys [name description active?]}]
  [:> Card {:class "p-4 mb-3"}
   [:> Flex {:justify "between" :align "center"}
    [:> Flex {:direction "column" :gap "1"}
     [:> Text {:size "3" :weight "medium"} name]
     [:> Text {:size "2" :color "gray"}
      (or description "No description")]]

    [:> Flex {:align "center" :gap "3"}
     ;; Status badge (online/offline)
     (if active?
       [:> Badge {:color "green" :variant "soft" :class "mr-3"} "Online"]
       [:> Badge {:color "red" :variant "soft" :class "mr-3"} "Offline"])

     ;; Edit button
     [:> Button {:variant "outline"
                 :onClick #(rf/dispatch [:navigate :access-control-edit {:group-id name}])}
      "Edit"]

     ;; Action menu (three dots)
     [:> Box {:class "ml-2"}
      [:> dropdown-menu/Root
       [:> dropdown-menu/Trigger {:asChild true}
        [:> IconButton {:variant "ghost" :color "gray"}
         [:> hero-outline/EllipsisHorizontalIcon {:className "h-5 w-5"}]]]

       [:> dropdown-menu/Portal
        [:> dropdown-menu/Content {:class "bg-white rounded-lg shadow-lg p-1 min-w-[160px] z-50"}
         [:> dropdown-menu/Item
          {:class "text-sm px-3 py-2 cursor-pointer rounded hover:bg-gray-100 flex items-center"
           :onClick #(rf/dispatch [:navigate :access-control-edit {:group-id name}])}
          "Edit"]

         [:> Separator {:class "my-1"}]

         [:> dropdown-menu/Item
          {:class "text-sm px-3 py-2 cursor-pointer rounded hover:bg-gray-100 text-red-500 flex items-center"
           :onClick #(rf/dispatch [:dialog->open
                                   {:title "Delete Group"
                                    :text (str "Are you sure you want to delete the group '" name "'? This action cannot be undone.")
                                    :text-action-button "Delete"
                                    :action-button? true
                                    :type :danger
                                    :on-success (fn []
                                                  (rf/dispatch [:access-control/delete-group name]))}])}
          "Delete"]]]]]]]])

(defn main []
  (let [user-groups (rf/subscribe [:user-groups])
        access-control-plugin (rf/subscribe [:access-control/plugin])
        groups-with-permissions (rf/subscribe [:access-control/groups-with-permissions])
        all-connections (rf/subscribe [:connections])]

    (fn []
      (let [processed-groups (map (fn [group]
                                    (let [group-name (get group "name" (:name group))]
                                      {:name group-name
                                       :description (get group "description"
                                                         (str "Group with "
                                                              (count (get @groups-with-permissions group-name []))
                                                              " connection permissions"))
                                       :active? (contains? @groups-with-permissions group-name)}))
                                  @user-groups)]
        [:> Box {:class "w-full"}
         [:> Flex {:justify "between" :align "center" :class "mb-6"}
          [:> Text {:size "5" :weight "bold"} "User Groups"]
          [:> Button {:size "3"
                      :class "bg-blue-600 hover:bg-blue-700"
                      :onClick #(rf/dispatch [:navigate :access-control-new])}
           "Create Group"]]

         ;; Se não há grupos, mostrar uma mensagem
         (if (empty? processed-groups)
           [:> Box {:class "text-center py-16 bg-white rounded-lg shadow-sm"}
            [:> Text {:size "3" :class "text-gray-11"}
             "No user groups found. Create a group to manage connection permissions."]]

           ;; Lista de grupos
           [:> Box {:class "space-y-2"}
            (for [group processed-groups]
              ^{:key (:name group)}
              [group-item group])])]))))
