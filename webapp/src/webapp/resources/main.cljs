(ns webapp.resources.main
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

(defn empty-list-view []
  [:> Box {:class "flex flex-col h-full items-center justify-between py-16 px-4 max-w-3xl mx-auto"}

   [:> Flex {:direction "column" :gap "3" :align "center"}
    [:> Box {:class "mb-8"}
     [:img {:src "/images/illustrations/empty-state.png"
            :alt "Empty state illustration"
            :class "w-96"}]]

    [:> Flex {:direction "column" :align "center" :gap "3" :class "text-center"}
     [:> Text {:size "3" :class "text-gray-11 max-w-md text-center"}
      "No resources found with these filters"]]

    [:> Button {:size "3"
                :onClick #(rf/dispatch [:navigate :resource-catalog])}
     "Setup new Resource"]]

   [:> Flex {:align "center"}
    [:> Text {:class "text-gray-11 mr-1"}
     "Need more information? Check out"]
    [:a {:href (config/docs-url :introduction :getting-started)
         :class "text-blue-600 hover:underline"}
     "getting started documentation"]
    [:> Text {:class "text-gray-11 ml-1"}
     "."]]])

(defn- loading-list-view []
  [:> Box {:class "flex items-center justify-center h-96"}
   [loaders/simple-loader]])

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
        [:> Box {:class "flex items-center justify-center rounded-full h-5 w-5 bg-gray-11"}
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
           :onClick #(on-change (if (= selected-resource value)
                                  nil
                                  value))}
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

;; Resources List Content (pure rendering, no infinite-scroll)
(defn resources-list-content [resources-data user]
  [:<>
   (doall
    (for [resource resources-data]
      ^{:key (:id resource)}
      [:> Box {:class (str "bg-white border border-[--gray-3] "
                           "first:rounded-t-lg last:rounded-b-lg "
                           "p-regular text-xs flex justify-between items-center")}
       [:> Box {:class "flex items-center gap-regular"}
        [:> Box
         [:figure {:class "w-6"}
          [:img {:src (connection-constants/get-connection-icon resource)
                 :class "w-9"
                 :loading "lazy"}]]]

        [:> Box
         [:> Text {:size "3" :weight "medium" :class "text-gray-12"}
          (:name resource)]]]

       (when (-> user :data :admin?)
         [:> Button {:size "2"
                     :variant "soft"
                     :color "gray"
                     :on-click #(rf/dispatch [:navigate :configure-resource {} :resource-id (:name resource)])}
          "Configure"])]))])


;; Roles List Content (pure rendering, no infinite-scroll)
(defn roles-list-content [connections-data user test-connection-state]
  [:<>
   (doall
    (for [connection connections-data]
      ^{:key (:id connection)}
      [:> Box {:class (str "bg-white border border-[--gray-3] "
                           "text-[--gray-12] "
                           "first:rounded-t-lg last:rounded-b-lg "
                           "first:border-t last:border-b "
                           "p-regular text-xs flex gap-8 justify-between items-center")}
       [:> Box {:class "flex truncate items-center gap-regular"}
        [:> Box
         [:figure {:class "w-6"}
          [:img {:src (connection-constants/get-connection-icon connection)
                 :class "w-9"
                 :loading "lazy"}]]]
        [:> Box
         [:> Text {:as "p" :size "3" :weight "medium" :class "text-gray-12"}
          (:name connection)]
         [:> Text {:as "p" :size "1" :class "text-gray-11"}
          (:resource_name connection)]
         [:> Text {:size "1" :class "flex items-center gap-1 text-gray-11"}
          [:> Box {:class (str "rounded-full h-[6px] w-[6px] "
                               (if (= (:status connection) "online")
                                 "bg-green-500"
                                 "bg-red-500"))}]
          (cs/capitalize (:status connection))]]]

       [:> Box {:class "flex gap-6 items-center"}
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
                                     :disabled (is-connection-testing? test-connection-state (:name connection))}
               "Test Connection"])]])

        (when (-> user :data :admin?)
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
             "Delete"]]])]]))])


;; Main component with custom tabs and filters in same row
(defn panel []
  (let [user (rf/subscribe [:users->current-user])
        resources (rf/subscribe [:resources->pagination])
        connections (rf/subscribe [:connections->pagination])
        test-connection-state (rf/subscribe [:connections->test-connection])
        search-string (.. js/window -location -search)
        url-params (new js/URLSearchParams search-string)
        initial-tab (.get url-params "tab")
        active-tab (r/atom (or initial-tab "roles"))
        connections-search-status (r/atom nil)
        search-name (r/atom "")
        selected-tags (r/atom {})
        tags-popover-open? (r/atom false)
        selected-resource (r/atom nil)
        search-debounce-timer (r/atom nil)
        connections-metadata (rf/subscribe [:connections->metadata])]

    ;; Initial load
    (rf/dispatch [:resources/get-resources-paginated {:force-refresh? true}])
    (rf/dispatch [:connections/get-connections-paginated {:force-refresh? true}])
    (rf/dispatch [:connections->get-connection-tags])
    (rf/dispatch [:guardrails->get-all])

    (when (nil? @connections-metadata)
      (rf/dispatch [:connections->load-metadata]))

    (when (empty? (:data @user))
      (rf/dispatch [:users->get-user]))

    (fn []
      (let [resources-state @resources
            connections-state @connections
            resources-data (:data resources-state)
            connections-data (:data connections-state)
            resources-loading? (= :loading (:loading resources-state))
            connections-loading? (= :loading (:loading connections-state))
            ;; Conditional logic based on active tab
            current-state (if (= @active-tab "resources") resources-state connections-state)
            current-data (if (= @active-tab "resources") resources-data connections-data)
            current-loading? (if (= @active-tab "resources") resources-loading? connections-loading?)
            any-filters? (or (seq @selected-tags) @selected-resource)

            apply-filter (fn [filter-update]
                           (when @search-debounce-timer
                             (js/clearTimeout @search-debounce-timer))
                           (reset! search-debounce-timer nil)
                           (reset! connections-search-status :loading)
                           (let [search-value (cs/trim @search-name)
                                 request (cond-> {:filters filter-update
                                                  :page 1
                                                  :force-refresh? true}
                                           (and (not (cs/blank? search-value))
                                                (> (count search-value) 2)) (assoc :search search-value))]
                             (if (= @active-tab "resources")
                               (rf/dispatch [:resources/get-resources-paginated request])
                               (rf/dispatch [:connections/get-connections-paginated request]))))]

        [infinite-scroll
         {:on-load-more (fn []
                          (when-not current-loading?
                            (if (= @active-tab "resources")
                              (rf/dispatch [:resources/get-resources-paginated
                                            {:page (inc (:current-page resources-state 1))
                                             :force-refresh? false}])
                              (rf/dispatch [:connections/get-connections-paginated
                                            {:page (inc (:current-page connections-state 1))
                                             :force-refresh? false}]))))
          :has-more? (:has-more? current-state)
          :loading? current-loading?}

         ^{:key "infinite-scroll-children"}
         [:> Box
          ;; Add button (admin only)
          (when (-> @user :data :admin?)
            [:> Box {:class "absolute top-10 right-4 sm:right-6 lg:top-12 lg:right-10"}
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
              (reset! selected-resource nil)
              (when @search-debounce-timer
                (js/clearTimeout @search-debounce-timer))
              (reset! search-debounce-timer nil)
              ;; Reset pagination state for the new tab
              (if (= new-tab "resources")
                (rf/dispatch [:resources/get-resources-paginated
                              {:page 1 :force-refresh? true :filters {}}])
                (rf/dispatch [:connections/get-connections-paginated
                              {:page 1 :force-refresh? true :filters {}}])))]

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
                                                                            :search trimmed :filters {}}])
                                                             (rf/dispatch [:connections/get-connections-paginated
                                                                           {:page 1 :force-refresh? true
                                                                            :search trimmed :filters {}}])))
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
                    [:> Box {:class "flex items-center justify-center rounded-full h-5 w-5 bg-gray-11"}
                     [:> Text {:size "1" :weight "bold" :class "text-white"}
                      (apply + (map count (vals @selected-tags)))]])]]]
               [:> Popover.Content {:size "2" :align "start" :style {:width "300px"}}
                [tag-selector/tag-selector @selected-tags
                 (fn [new-selected]
                   (reset! selected-tags new-selected)
                   (apply-filter (cond-> {}
                                   (not-empty new-selected) (assoc :tag_selector (tag-selector/tags-to-query-string new-selected))
                                   @selected-resource (assoc :subtype @selected-resource))))]]])

            ;; Resource Type
            [resource-type-component @selected-resource
             (fn [resource]
               (reset! selected-resource resource)
               (apply-filter (cond-> {}
                               (not-empty @selected-tags) (assoc :tag_selector (tag-selector/tags-to-query-string @selected-tags))
                               resource (assoc :subtype resource))))]]]

          ;; Test Connection Modal (for roles tab)
          (when (= @active-tab "roles")
            [test-connection-modal/test-connection-modal
             (get-in @test-connection-state [:connection-name])])

          ;; Tab Content (just the list content, no wrappers)
          [:> Box {:class "flex-1 h-full"}
           (cond
             ;; Loading state when no data
             (and current-loading? (empty? current-data))
             [loading-list-view]

             ;; Empty state
             (and (empty? current-data) (not current-loading?))
             [empty-list-view (if (= @active-tab "resources") "resources" "roles")]

             ;; Content
             :else
             (if (= @active-tab "resources")
               [resources-list-content resources-data @user]
               [roles-list-content connections-data @user @test-connection-state]))]]]))))
