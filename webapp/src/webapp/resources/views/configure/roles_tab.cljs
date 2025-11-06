(ns webapp.resources.views.configure.roles-tab
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.components.loaders :as loaders]
   [webapp.components.infinite-scroll :refer [infinite-scroll]]
   [webapp.connections.constants :as connection-constants]
   [webapp.connections.helpers :refer [is-connection-testing?]]
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
        test-connection-state (rf/subscribe [:connections->test-connection])]

    (rf/dispatch [:resources->get-resource-roles resource-id {:force-refresh? true}])

    (fn [resource-id]
      (let [roles-state-data (or @roles-state {:loading true :data []})
            {:keys [loading data has-more? current-page]} roles-state-data
            roles (or data [])
            roles-loading? (= :loading loading)]

        [:> Box
         ;; Test Connection Modal
         [test-connection-modal/test-connection-modal
          (get-in @test-connection-state [:connection-name])]

         ;; Content
         (cond
           (and roles-loading? (empty? roles))
           [loading-view]

           (and (empty? roles) (not roles-loading?))
           [empty-roles-view]

           :else
           [:div {:class "h-full overflow-y-auto"}
            [infinite-scroll
             {:on-load-more #(when-not roles-loading?
                               (rf/dispatch [:resources->get-resource-roles
                                             resource-id
                                             {:page (inc (or current-page 1))
                                              :force-refresh? false}]))
              :has-more? has-more?
              :loading? roles-loading?}
             (doall
              (for [connection roles]
                ^{:key (:id connection)}
                [:> Box {:class (str "bg-white border border-[--gray-3] "
                                     "text-[--gray-12] "
                                     "first:rounded-t-lg last:rounded-b-lg "
                                     "first:border-t last:border-b "
                                     "p-regular text-xs flex gap-8 justify-between items-center")}
                 [:div {:class "flex truncate items-center gap-regular"}
                  [:div
                   [:figure {:class "w-6"}
                    [:img {:src (connection-constants/get-connection-icon connection)
                           :class "w-9"
                           :loading "lazy"}]]]
                  [:div
                   [:> Text {:as "p" :size "3" :weight "medium" :class "text-gray-12"}
                    (:name connection)]
                   [:> Text {:as "p" :size "1" :class "text-gray-11"}
                    (:resource_name connection)]
                   [:> Text {:size "1" :class "flex items-center gap-1 text-gray-11"}
                    [:div {:class (str "rounded-full h-[6px] w-[6px] "
                                       (if (= (:status connection) "online")
                                         "bg-green-500"
                                         "bg-red-500"))}]
                    (cs/capitalize (:status connection))]]]

                 [:> Flex {:gap "3" :justify "between" :align "center"}
                  [:> Button {:size "2"
                              :variant "outline"
                              :color "red"
                              :on-click (fn []
                                          (rf/dispatch [:dialog->open
                                                        {:title "Delete role?"
                                                         :type :danger
                                                         :text-action-button "Confirm and delete"
                                                         :action-button? true
                                                         :text [:> Box {:class "space-y-radix-4"}
                                                                [:> Text {:as "p"}
                                                                 "This action will instantly remove your access to "
                                                                 (:name connection)
                                                                 " and can not be undone."]
                                                                [:> Text {:as "p"}
                                                                 "Are you sure you want to delete this role?"]]
                                                         :on-success (fn []
                                                                       (rf/dispatch [:connections->delete-connection (:name connection)])
                                                                       (rf/dispatch [:resources->get-resource-roles resource-id {:force-refresh? true}])
                                                                       (rf/dispatch [:modal->close]))}]))}
                   "Delete"]
                  [:> Button {:size "2"
                              :variant "outline"
                              :color "gray"
                              :on-click (fn []
                                          (rf/dispatch [:navigate :configure-role {:from_page "resource-configure"} :connection-name (:name connection)]))}
                   "Configure"]
                  [:> Button {:size "2"
                              :variant "soft"
                              :color "indigo"
                              :on-click #(rf/dispatch [:connections->test-connection (:name connection)])
                              :disabled (is-connection-testing? @test-connection-state (:name connection))}
                   "Test Connection"]]]))]])]))))
