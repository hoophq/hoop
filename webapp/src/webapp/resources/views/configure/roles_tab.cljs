(ns webapp.resources.views.configure.roles-tab
  (:require
   ["@radix-ui/themes" :refer [Box Button DropdownMenu Flex Text Separator]]
   ["lucide-react" :refer [EllipsisVertical Plus]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.components.loaders :as loaders]
   [webapp.connections.constants :as connection-constants]
   [webapp.connections.helpers :refer [can-test-connection? is-connection-testing?]]
   [webapp.connections.views.test-connection-modal :as test-connection-modal]))

(defn empty-roles-view []
  [:div {:class "py-16 text-center"}
   [:> Text {:size "3" :weight "bold" :class "text-gray-12 mb-2"}
    "No roles configured yet"]
   [:> Text {:size "2" :class "text-gray-11"}
    "Add a new role to get started."]])

(defn loading-view []
  [:div {:class "flex items-center justify-center py-16"}
   [loaders/simple-loader]])

(defn main [resource-id]
  (let [roles-state (rf/subscribe [:resources->resource-roles resource-id])
        test-connection-state (rf/subscribe [:connections->test-connection])
        user (rf/subscribe [:users->current-user])]

    ;; Fetch roles on mount
    (when (nil? @roles-state)
      (rf/dispatch [:resources->get-resource-roles resource-id]))

    (fn [resource-id]
      (let [{:keys [loading data]} (or @roles-state {:loading true :data []})
            roles (or data [])]

        [:> Box
         ;; Test Connection Modal
         [test-connection-modal/test-connection-modal
          (get-in @test-connection-state [:connection-name])]

         ;; Content
         (cond
           loading
           [loading-view]

           (empty? roles)
           [empty-roles-view]

           :else
           [:> Box {:class "space-y-4"}
            ;; Roles list
            (doall
             (for [role roles]
               ^{:key (:id role)}
               [:> Box {:class (str "bg-white border border-[--gray-3] "
                                    "rounded-lg p-radix-4 flex justify-between items-center")}
                ;; Left side - Role info
                [:div {:class "flex items-center gap-4"}
                 [:figure {:class "w-6"}
                  [:img {:src (connection-constants/get-connection-icon role)
                         :class "w-9"
                         :loading "lazy"}]]

                 [:div
                  [:> Text {:as "p" :size "3" :weight "medium" :class "text-gray-12"}
                   (:name role)]
                  [:> Text {:as "p" :size "1" :class "text-gray-11"}
                   (str "Resource: " (:resource_name role))]
                  [:> Text {:size "1" :class "flex items-center gap-1 text-gray-11"}
                   [:div {:class (str "rounded-full h-[6px] w-[6px] "
                                      (if (= (:status role) "online")
                                        "bg-green-500"
                                        "bg-red-500"))}]
                   (cs/capitalize (:status role))]]]

                ;; Right side - Actions
                [:> Flex {:gap "3" :align "center"}
                 ;; Test Connection button
                 (when (can-test-connection? role)
                   [:> Button {:size "2"
                               :variant "soft"
                               :color "gray"
                               :disabled (is-connection-testing? @test-connection-state (:name role))
                               :on-click #(rf/dispatch [:connections->test-connection (:name role)])}
                    "Test Connection"])

                 ;; Admin actions dropdown
                 (when (-> @user :data :admin?)
                   [:> DropdownMenu.Root {:dir "rtl"}
                    [:> DropdownMenu.Trigger
                     [:> Button {:size "2" :variant "ghost" :color "gray"}
                      [:> EllipsisVertical {:size 16}]]]
                    [:> DropdownMenu.Content
                     ;; Configure
                     (when (not (= (:managed_by role) "hoopagent"))
                       [:> DropdownMenu.Item {:on-click
                                              (fn []
                                                (rf/dispatch [:plugins->get-my-plugins])
                                                (rf/dispatch [:navigate :edit-connection {} :connection-name (:name role)]))}
                        "Configure"])
                     ;; Delete
                     [:> DropdownMenu.Item {:color "red"
                                            :on-click (fn []
                                                        (rf/dispatch [:dialog->open
                                                                      {:title "Delete role?"
                                                                       :type :danger
                                                                       :text-action-button "Confirm and delete"
                                                                       :action-button? true
                                                                       :text [:> Box {:class "space-y-radix-4"}
                                                                              [:> Text {:as "p"}
                                                                               "This action will instantly remove access to "
                                                                               (:name role)
                                                                               " and cannot be undone."]
                                                                              [:> Text {:as "p"}
                                                                               "Are you sure you want to delete this role?"]]
                                                                       :on-success (fn []
                                                                                     (rf/dispatch [:connections->delete-connection (:name role)])
                                                                                     (rf/dispatch [:resources->get-resource-roles resource-id])
                                                                                     (rf/dispatch [:dialog->close]))}]))}
                      "Delete"]]])]]))

            ;; Separator before Add button
            [:> Separator {:size "4" :class "my-6"}]

            ;; Add new role button
            (when (-> @user :data :admin?)
              [:> Flex {:justify "start"}
               [:> Button {:size "3"
                           :variant "soft"
                           :on-click #(js/console.log "Add new role for resource:" resource-id)}
                [:> Plus {:size 16}]
                "Add New Role"]])])]))))

