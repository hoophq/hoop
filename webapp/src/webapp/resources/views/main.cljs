(ns webapp.resources.views.main
  (:require ["lucide-react" :refer [EllipsisVertical Tag Shapes Check Search]]
            ["@radix-ui/themes" :refer [IconButton Box Button DropdownMenu
                                        Flex Text Popover TextField]]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.loaders :as loaders]
            [webapp.components.infinite-scroll :refer [infinite-scroll]]
            [webapp.connections.constants :as connection-constants]
            [webapp.resources.helpers :refer [can-open-web-terminal?]]
            [webapp.connections.helpers :refer [can-test-connection? is-connection-testing?
                                                can-connect? can-hoop-cli? can-access-native-client?]]
            [webapp.connections.views.hoop-cli-modal :as hoop-cli-modal]
            [webapp.connections.views.tag-selector :as tag-selector]
            [webapp.connections.views.test-connection-modal :as test-connection-modal]
            [webapp.config :as config]))

(defn empty-list-view [type]
  [:div {:class "pt-x-large"}
   [:figure
    {:class "w-1/6 mx-auto p-regular"}
    [:img {:src (str config/webapp-url "/images/illustrations/pc.svg")
           :class "w-full"}]]
   [:div {:class "px-large text-center"}
    [:div {:class "text-gray-700 text-sm font-bold"}
     (str "No " type " found")]
    [:div {:class "text-gray-500 text-xs mb-large"}
     "There's nothing with this criteria"]]])

(defn- loading-list-view []
  [:div {:class "flex items-center justify-center rounded-lg border bg-white h-full"}
   [:div {:class "flex items-center justify-center h-full"}
    [loaders/simple-loader]]])

(def resource-types
  [{:id "postgres" :value "postgres" :label "PostgreSQL"}
   {:id "mysql" :value "mysql" :label "MySQL"}
   {:id "mongodb" :value "mongodb" :label "MongoDB"}
   {:id "mssql" :value "mssql" :label "MSSQL"}
   {:id "oracledb" :value "oracledb" :label "OracleDB"}
   {:id "ssh" :value "ssh" :label "SSH"}
   {:id "tcp" :value "tcp" :label "TCP"}])

(defn resource-type-component [selected-resource on-change]
  [:> Popover.Root
   [:> Popover.Trigger {:asChild true}
    [:> Button {:size "2"
                :variant (if selected-resource "soft" "surface")
                :color "gray"}
     [:> Flex {:gap "2" :align "center"}
      [:> Shapes {:size 16}]
      "Resource"
      (when selected-resource
        [:div {:class "flex items-center justify-center rounded-full h-5 w-5 bg-gray-11"}
         [:> Text {:size "1" :weight "bold" :class "text-white"}
          "1"]])]]]

   [:> Popover.Content {:size "2" :style {:width "280px"}}
    [:> Box {:class "w-full max-h-64 overflow-y-auto"}
     [:> Box {:class "space-y-1 px-2 pt-2"}
      (doall
       (for [{:keys [id value label]} resource-types]
         ^{:key id}
         [:> Button
          {:variant "ghost"
           :color "gray"
           :class "w-full justify-between gap-2"
           :onClick #(on-change (if (= selected-resource value) nil value))}
          [:> Text {:size "2" :class "text-gray-12"}
           label]
          (when (= selected-resource value)
            [:> Check {:size 16}])]))]]]])

;; Custom Tab Header Component
(defn custom-tab-header [active-tab on-change]
  [:> Flex {:gap "4" :align "center"}
   [:> Text {:as "h2"
             :size "6"
             :weight "bold"
             :class (str "cursor-pointer transition-colors "
                         (if (= active-tab "resources")
                           "text-gray-12"
                           "text-gray-8 hover:text-gray-11"))
             :onClick #(on-change "resources")}
    "My Resources"]
   [:> Text {:as "h2"
             :size "6"
             :weight "bold"
             :class (str "cursor-pointer transition-colors "
                         (if (= active-tab "roles")
                           "text-gray-12"
                           "text-gray-8 hover:text-gray-11"))
             :onClick #(on-change "roles")}
    "My Roles"]])

;; Resources List (without filters)
(defn resources-list []
  (let [resources (rf/subscribe [:resources->pagination])
        user (rf/subscribe [:users->current-user])]
    (fn []
      (let [resources-state @resources
            resources-data (:data resources-state)
            resources-loading? (= :loading (:loading resources-state))]

        (if (and resources-loading? (empty? resources-data))
          [loading-list-view]
          [:div {:class "h-full overflow-y-auto"}
           (when (and (empty? resources-data) (not resources-loading?))
             [empty-list-view "resources"])

           (when (seq resources-data)
             [infinite-scroll
              {:on-load-more #(when-not resources-loading?
                                (rf/dispatch [:resources/get-resources-paginated
                                              {:page (inc (:current-page resources-state 1))
                                               :force-refresh? false}]))
               :has-more? (:has-more? resources-state)
               :loading? resources-loading?}
              (doall
               (for [resource resources-data]
                 ^{:key (:id resource)}
                 [:> Box {:class (str "bg-white border border-[--gray-3] "
                                      "first:rounded-t-lg last:rounded-b-lg "
                                      "p-regular text-xs flex justify-between items-center")}
                  [:div {:class "flex items-center gap-regular"}
                   [:div
                    [:figure {:class "w-6"}
                     [:img {:src (connection-constants/get-connection-icon resource)
                            :class "w-9"
                            :loading "lazy"}]]]

                   [:div
                    [:> Text {:size "3" :weight "medium" :class "text-gray-12"}
                     (:name resource)]]]

                  (when (-> @user :data :admin?)
                    [:> Button {:size "2"
                                :variant "soft"
                                :color "gray"
                                :on-click #(rf/dispatch [:navigate :configure-resource {} :resource-id (:name resource)])}
                     "Configure"])]))])])))))

;; Roles List (without filters)
(defn roles-list []
  (let [connections (rf/subscribe [:connections->pagination])
        user (rf/subscribe [:users->current-user])
        test-connection-state (rf/subscribe [:connections->test-connection])]
    (fn []
      (let [connections-state @connections
            connections-data (:data connections-state)
            connections-loading? (= :loading (:loading connections-state))]

        [:div {:class "flex flex-col h-full"}
         ;; Test Connection Modal
         [test-connection-modal/test-connection-modal
          (get-in @test-connection-state [:connection-name])]

         ;; List
         (if (and connections-loading? (empty? connections-data))
           [loading-list-view]
           [:div {:class "h-full overflow-y-auto"}
            (when (and (empty? connections-data) (not connections-loading?))
              [empty-list-view "roles"])

            (when (seq connections-data)
              [infinite-scroll
               {:on-load-more #(when-not connections-loading?
                                 (rf/dispatch [:connections/get-connections-paginated
                                               {:page (inc (:current-page connections-state 1))
                                                :force-refresh? false}]))
                :has-more? (:has-more? connections-state)
                :loading? connections-loading?}
               (doall
                (for [connection connections-data]
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

                   [:div {:class "flex gap-6 items-center"}
                    (when (can-connect? connection)
                      [:> DropdownMenu.Root {:dir "rtl"}
                       [:> DropdownMenu.Trigger
                        [:> Button {:size 2 :variant "soft"}
                         "Connect"
                         [:> DropdownMenu.TriggerIcon]]]
                       [:> DropdownMenu.Content
                        (when (can-open-web-terminal? connection)
                          [:> DropdownMenu.Item {:on-click
                                                 (fn []
                                                   (js/localStorage.setItem "selected-connection" connection)
                                                   (rf/dispatch [:database-schema->clear-schema])
                                                   (rf/dispatch [:navigate :editor-plugin-panel]))}
                           "Open in Web Terminal"])

                        (when (can-hoop-cli? connection)
                          [:> DropdownMenu.Item {:on-click
                                                 #(rf/dispatch [:modal->open
                                                                {:content [hoop-cli-modal/main (:name connection)]
                                                                 :maxWidth "1100px"
                                                                 :class "overflow-hidden"}])}
                           "Open with Hoop CLI"])

                        (when (can-access-native-client? connection)
                          [:> DropdownMenu.Item {:on-click
                                                 #(rf/dispatch [:native-client-access->start-flow (:name connection)])}
                           "Open in Native Client"])

                        (when (can-test-connection? connection)
                          [:> DropdownMenu.Item {:on-click #(rf/dispatch [:connections->test-connection (:name connection)])
                                                 :disabled (is-connection-testing? @test-connection-state (:name connection))}
                           "Test Connection"])]])

                    (when (-> @user :data :admin?)
                      [:> DropdownMenu.Root {:dir "rtl"}
                       [:> DropdownMenu.Trigger
                        [:> IconButton {:size "1" :variant "ghost" :color "gray"}
                         [:> EllipsisVertical {:size 16}]]]
                       [:> DropdownMenu.Content
                        (when (not (= (:managed_by connection) "hoopagent"))
                          [:> DropdownMenu.Item {:on-click
                                                 (fn []
                                                   (rf/dispatch [:navigate :configure-role {:from_page "roles-list"} :connection-name (:name connection)]))}
                           "Configure"])
                        [:> DropdownMenu.Item {:color "red"
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
                                                                                        (rf/dispatch [:modal->close]))}]))}
                         "Delete"]]])]]))])])]))))

;; Main component with custom tabs and filters in same row
(defn panel []
  (let [user (rf/subscribe [:users->current-user])
        active-tab (r/atom "resources")
        search-name (r/atom "")
        selected-tags (r/atom {})
        tags-popover-open? (r/atom false)
        selected-resource (r/atom nil)
        search-debounce-timer (r/atom nil)]

    ;; Initial load
    (rf/dispatch [:resources/get-resources-paginated {:force-refresh? true}])
    (rf/dispatch [:connections/get-connections-paginated {:force-refresh? true}])
    (rf/dispatch [:connections->get-connection-tags])
    (rf/dispatch [:guardrails->get-all])

    (when (empty? (:data @user))
      (rf/dispatch [:users->get-user]))

    (fn []
      (let [any-filters? (or (seq @selected-tags) @selected-resource)]
        [:div {:class "flex flex-col h-full"}
         ;; Add button (admin only)
         (when (-> @user :data :admin?)
           [:div {:class "absolute top-10 right-4 sm:right-6 lg:top-12 lg:right-10"}
            [:> Button {:on-click #(rf/dispatch [:navigate :resource-catalog])}
             "Setup new Resource"]])

         ;; Tab Header and Filters in SAME ROW
         [:> Flex {:justify "between" :align "center" :class "mb-4"}
          ;; LEFT: Custom Tab Headers
          [custom-tab-header @active-tab
           (fn [new-tab]
             (reset! active-tab new-tab)
             (reset! search-name "")
             (reset! selected-tags {})
             (reset! selected-resource nil))]

          ;; RIGHT: Filters
          [:> Flex {:gap "2"}
           ;; Clear Filters
           (when any-filters?
             [:> Button {:size "2" :variant "soft" :color "gray"
                         :on-click (fn []
                                     (reset! selected-tags {})
                                     (reset! selected-resource nil)
                                     (reset! search-name "")
                                     (when @search-debounce-timer
                                       (js/clearTimeout @search-debounce-timer))
                                     (if (= @active-tab "resources")
                                       (rf/dispatch [:resources/get-resources-paginated
                                                     {:page 1 :force-refresh? true :filters {}}])
                                       (rf/dispatch [:connections/get-connections-paginated
                                                     {:page 1 :force-refresh? true :filters {}}])))}
              "Clear Filters"])

           ;; Search
           [:> TextField.Root {:placeholder (if (= @active-tab "resources")
                                              "Search resources"
                                              "Search roles")
                               :value @search-name
                               :onChange (fn [e]
                                           (let [value (-> e .-target .-value)
                                                 trimmed (cs/trim value)]
                                             (reset! search-name value)
                                             (when @search-debounce-timer
                                               (js/clearTimeout @search-debounce-timer))
                                             (when (or (cs/blank? trimmed) (> (count trimmed) 2))
                                               (reset! search-debounce-timer
                                                       (js/setTimeout
                                                        (fn []
                                                          (if (= @active-tab "resources")
                                                            (rf/dispatch [:resources/get-resources-paginated
                                                                          {:page 1 :force-refresh? true
                                                                           :name trimmed :filters {}}])
                                                            (rf/dispatch [:connections/get-connections-paginated
                                                                          {:page 1 :force-refresh? true
                                                                           :name trimmed :filters {}}])))
                                                        500)))))}
            [:> TextField.Slot [:> Search {:size 16}]]]

           ;; Tags (only for roles)
           (when (= @active-tab "roles")
             [:> Popover.Root {:open @tags-popover-open?
                               :onOpenChange #(reset! tags-popover-open? %)}
              [:> Popover.Trigger {:asChild true}
               [:> Button {:size "2"
                           :variant (if (not-empty @selected-tags) "soft" "surface")
                           :color "gray"}
                [:> Flex {:gap "2" :align "center"}
                 [:> Tag {:size 16}] "Tags"
                 (when (not-empty @selected-tags)
                   [:div {:class "flex items-center justify-center rounded-full h-5 w-5 bg-gray-11"}
                    [:> Text {:size "1" :weight "bold" :class "text-white"}
                     (apply + (map count (vals @selected-tags)))]])]]]
              [:> Popover.Content {:size "2" :align "start" :style {:width "300px"}}
               [tag-selector/tag-selector @selected-tags
                (fn [new-selected]
                  (reset! selected-tags new-selected))]]])

           ;; Resource Type
           [resource-type-component @selected-resource
            (fn [resource] (reset! selected-resource resource))]]]

         ;; Tab Content (just the list, no filters)
         [:div {:class "flex-1 overflow-hidden"}
          (case @active-tab
            "resources" [resources-list]
            "roles" [roles-list]
            [resources-list])]]))))
