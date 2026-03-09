(ns webapp.resources.main
  (:require ["lucide-react" :refer [EllipsisVertical Tag Shapes Search]]
            ["@radix-ui/themes" :refer [IconButton Box Button DropdownMenu
                                        Flex Text Popover TextField Link Tabs Heading Select]]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.loaders :as loaders]
            [webapp.components.infinite-scroll :refer [infinite-scroll]]
            [webapp.connections.constants :as connection-constants]
            [webapp.resources.helpers :refer [can-open-web-terminal?
                                              can-test-connection? is-connection-testing?
                                              can-connect? can-hoop-cli? can-access-native-client?]]
            [webapp.connections.views.hoop-cli-modal :as hoop-cli-modal]
            [webapp.connections.views.tag-selector :as tag-selector]
            [webapp.connections.views.test-connection-modal :as test-connection-modal]
            [webapp.config :as config]))

(defn empty-list-view [is-admin?]
  [:<>
   [:> Box {:class "flex flex-col flex-1 h-full items-center justify-center"}

    [:> Flex {:direction "column" :gap "6" :align "center"}
     [:> Box {:class "w-80"}
      [:img {:src "/images/illustrations/empty-state.png"
             :alt "Empty state illustration"}]]

     [:> Text {:size "3" :class "text-gray-11 text-center"}
      (if is-admin?
        "Get started by setting up your environment resources in your Organization"
        "Discover the resources your organization can connect to securely through Hoop")]

     [:> Button {:size "3"
                 :onClick #(rf/dispatch [:navigate :resource-catalog])}
      (if is-admin?
        "Setup new Resource"
        "Explore Resource Catalog")]]]

   [:> Flex {:align "center" :justify "center"}
    [:> Text {:size "2" :class "text-gray-11 mr-1"}
     "Need more information? Check out"]
    [:> Link {:size "2"
              :href (get-in config/docs-url [:introduction :getting-started])
              :target "_blank"}
     "getting started documentation"]
    [:> Text {:size "2" :class "text-gray-11"}
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

(defn- resource-name-map [resources-metadata]
  (reduce
   (fn [acc {:keys [id name]}]
     (cond-> acc
       (and (seq id) (seq name)) (assoc id name)))
   {}
   (:connections resources-metadata)))

(defn- icon-alt-text [item display-name-map]
  (let [subtype (:subtype item)]
    (or (get display-name-map subtype) subtype)))

(defn resource-type-component [selected-resource on-change]
  [:> Select.Root {:value (or selected-resource "all")
                   :onValueChange #(on-change (when-not (= % "all") %))}
   [:> Select.Trigger {:size "2"
                       :variant (if selected-resource "soft" "surface")
                       :color "gray"
                       :class "text-gray-11 [&_.rt-SelectIcon]:hidden"
                       :aria-label "Filter by resource type"}
    [:> Flex {:gap "2" :align "center"}
     [:> Shapes {:size 16 :aria-hidden "true" :class "text-gray-11"}]
     (or (some (fn [{:keys [value label]}]
                 (when (= value selected-resource) label))
               resource-types)
         "Resource")]]
   [:> Select.Content {:position "popper"}
    [:> Select.Item {:value "all"} "All resources"]
    (doall
     (for [{:keys [id value label]} resource-types]
       ^{:key id}
       [:> Select.Item {:value value} label]))]])

;; Custom Tab Header Component
(defn custom-tab-header []
  [:> Tabs.List {:size "2"
                 :aria-label "View selector"
                 :class "shadow-none"}
   [:> Tabs.Trigger {:value "resources"
                     :class "justify-start p-0 text-gray-8 hover:text-gray-11 transition-colors
                             [&_.rt-BaseTabListTriggerInner]:!p-0 [&_.rt-BaseTabListTriggerInner]:bg-transparent  hover:[&_.rt-BaseTabListTriggerInner]:bg-transparent
                             before:hidden data-[state=active]:text-gray-12"}
    [:> Text {:as "span"
              :size "6"
              :weight "bold"}
     "My Resources"]]

   [:> Tabs.Trigger {:value "roles"
                     :class "justify-start p-0 text-gray-8 hover:text-gray-11 transition-colors
                             [&_.rt-BaseTabListTriggerInner]:!p-0 [&_.rt-BaseTabListTriggerInner]:bg-transparent  hover:[&_.rt-BaseTabListTriggerInner]:bg-transparent
                             before:hidden data-[state=active]:text-gray-12"}
    [:> Text {:as "span"
              :size "6"
              :weight "bold"}
     "My Roles"]]])

(defn resources-list-content [resources-data user display-name-map]
  [:ul {:role "list" :class "list-none m-0 p-0"}
   (doall
    (for [resource resources-data]
      ^{:key (:id resource)}
      [:li {:class (str "bg-white border border-[--gray-3] "
                        "first:rounded-t-lg last:rounded-b-lg "
                        "p-regular text-xs flex justify-between items-center")}
       [:> Box {:class "flex items-center gap-regular"}
        [:img {:src (connection-constants/get-connection-icon resource)
               :alt (icon-alt-text resource display-name-map)
               :class "w-6"
               :loading "lazy"}]

        [:> Box
         [:> Heading {:as "h3"
                      :size "3"
                      :weight "medium"
                      :class "text-gray-12"}
          (:name resource)]]]

       (when (-> user :data :admin?)
         [:> Button {:size "2"
                     :variant "soft"
                     :color "gray"
                     :aria-label (str "Configure " (:name resource))
                     :on-click #(rf/dispatch [:navigate :configure-resource {} :resource-id (:name resource)])}
          "Configure"])]))])

(defn roles-list-content [connections-data user test-connection-state display-name-map]
  [:ul {:role "list" :class "list-none m-0 p-0"}
   (doall
    (for [connection connections-data]
      ^{:key (:id connection)}
      [:li {:class (str "bg-white border border-[--gray-3] "
                        "text-[--gray-12] "
                        "first:rounded-t-lg last:rounded-b-lg "
                        "first:border-t last:border-b "
                        "p-regular text-xs flex gap-8 justify-between items-center")}
       [:> Box {:class "flex truncate items-center gap-regular"}
        [:img {:src (connection-constants/get-connection-icon connection)
               :alt (icon-alt-text connection display-name-map)
               :class "w-6"
               :loading "lazy"}]
        [:> Box
         [:> Heading {:as "h3"
                      :size "3"
                      :weight "medium"
                      :class "text-gray-12"}
          (:name connection)]
         [:> Text {:as "p" :size "1" :class "text-gray-11"}
          (:resource_name connection)]
         [:> Text {:size "1" :class "flex items-center gap-1 text-gray-11"}
          [:> Box {:class (str "rounded-full h-[6px] w-[6px] "
                               (if (= (:status connection) "online")
                                 "bg-green-500"
                                 "bg-red-500"))
                   :aria-hidden "true"}]
          (cs/capitalize (:status connection))]]]

       [:> Box {:class "flex gap-6 items-center"}
        (when (can-connect? connection)
          [:> DropdownMenu.Root {:dir "rtl"}
           [:> DropdownMenu.Trigger
            [:> Button {:size 2
                        :variant "soft"
                        :aria-label (str "Connect to " (:name connection))}
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

            (when (and (can-hoop-cli? connection)
                       (not (can-access-native-client? connection)))
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
            [:> IconButton {:size "1"
                            :variant "ghost"
                            :color "gray"
                            :aria-label (str "More options for " (:name connection))}
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
            resource-names (resource-name-map @connections-metadata)
            resources-data (:data resources-state)
            connections-data (:data connections-state)
            resources-loading? (= :loading (:loading resources-state))
            connections-loading? (= :loading (:loading connections-state))
            ;; Conditional logic based on active tab
            current-loading? (if (= @active-tab "resources") resources-loading? connections-loading?)
            has-filters? (or (seq @selected-tags) @selected-resource)
            current-count (if (= @active-tab "resources")
                            (count resources-data)
                            (count connections-data))

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

        [:> Box {:class "flex flex-col px-10 pb-10"}
         [:> Tabs.Root
          {:value @active-tab
           :onValueChange (fn [new-tab]
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
                                            {:page 1 :force-refresh? true :filters {}}])))}

          ;; Sticky Header with Title, Tabs and Filters
          [:> Box {:class "sticky top-0 z-10 bg-gray-1 pt-10 pb-4 space-y-4"}
           ;; Title and Add button row
           [:> Flex {:justify "between" :align "center"}
            [:> Heading {:as "h1" :size "8" :weight "bold" :class "text-gray-12"}
             "Resources"]

            (when-not (empty? resources-data)
              [:> Button {:on-click #(rf/dispatch [:navigate :resource-catalog])}
               (if (-> @user :data :admin?)
                 "Setup new Resource"
                 "Explore Resource Catalog")])]

           ;; Tabs and Filters row
           [:> Flex {:justify "between" :align "center"}
            [custom-tab-header]

            [:> Flex {:gap "2"}
             (when has-filters?
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
                                 :aria-label (if (= @active-tab "resources")
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
                             :color "gray"
                             :aria-label (if (not-empty @selected-tags)
                                           (str "Filter by tags, "
                                                (apply + (map count (vals @selected-tags)))
                                                " selected")
                                           "Filter by tags")}
                  [:> Flex {:gap "2" :align "center"}
                   [:> Tag {:size 16
                            :aria-hidden "true"}]
                   "Tags"
                   (when (not-empty @selected-tags)
                     [:> Box {:class "flex items-center justify-center rounded-full h-5 w-5 bg-gray-11"
                              :aria-hidden "true"}
                      [:> Text {:size "1" :weight "bold" :class "text-white"}
                       (apply + (map count (vals @selected-tags)))]])]]]
                [:> Popover.Content {:size "2"
                                     :align "start"
                                     :style {:width "300px"}
                                     :aria-label "Tag filter options"}
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
                                resource (assoc :subtype resource))))]]

            [:div {:role "status"
                   :aria-live "polite"
                   :aria-atomic "true"
                   :class "sr-only"}
             (if current-loading?
               (str "Searching " @active-tab "...")
               (str current-count
                    " "
                    @active-tab
                    " found"
                    (when has-filters?
                      " with active filters")))]]]

          [:> Tabs.Content {:value "resources"}
           (cond
             ;; Loading state when no data
             (and resources-loading? (empty? resources-data))
             [:> Box {:class "flex-1 min-h-96 h-[calc(100vh-12rem)"}
              [loading-list-view]]

             ;; Empty state
             (and (empty? resources-data) (not resources-loading?))
             [:> Box {:class "flex flex-col min-h-96 h-[calc(100vh-12rem)"}
              [empty-list-view (-> @user :data :admin?)]]

             ;; Content
             :else
             [:> Box {:class "flex-1"}
              [resources-list-content resources-data @user resource-names]
              (when (:has-more? resources-state)
                [infinite-scroll
                 {:on-load-more (fn []
                                  (when-not resources-loading?
                                    (rf/dispatch [:resources/get-resources-paginated
                                                  {:page (inc (:current-page resources-state 1))
                                                   :force-refresh? false}])))
                  :has-more? (:has-more? resources-state)
                  :loading? resources-loading?}
                 [:div]])])]

          [:> Tabs.Content {:value "roles"}
           [test-connection-modal/test-connection-modal
            (get-in @test-connection-state [:connection-name])]
           (cond
             ;; Loading state when no data
             (and connections-loading? (empty? connections-data))
             [:> Box {:class "flex-1 min-h-96 h-[calc(100vh-12rem)"}
              [loading-list-view]]

             ;; Empty state
             (and (empty? connections-data) (not connections-loading?))
             [:> Box {:class "flex flex-col min-h-96 h-[calc(100vh-12rem)]"}
              [empty-list-view (-> @user :data :admin?)]]

             ;; Content
             :else
             [:> Box {:class "flex-1"}
              [roles-list-content connections-data @user @test-connection-state resource-names]
              (when (:has-more? connections-state)
                [infinite-scroll
                 {:on-load-more (fn []
                                  (when-not connections-loading?
                                    (rf/dispatch [:connections/get-connections-paginated
                                                  {:page (inc (:current-page connections-state 1))
                                                   :force-refresh? false}])))
                  :has-more? (:has-more? connections-state)
                  :loading? connections-loading?}
                 [:div]])])]]]))))
