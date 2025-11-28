(ns webapp.shared-ui.cmdk.command-palette-pages
  (:require
   ["cmdk" :refer [CommandGroup CommandItem CommandSeparator]]
   ["lucide-react" :refer [File ChevronRight Rotate3d Package]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.features.runbooks.helpers :as runbooks-helpers]
   [webapp.shared-ui.cmdk.command-palette-constants :as constants]))

(defn action-item
  "Generic action item component"
  [{:keys [id label icon requires-upgrade?] :as item}]
  [:> CommandItem
   {:key id
    :value label
    :keywords [label]
    :onSelect #(rf/dispatch [:command-palette->execute-action item])}
   [:div {:class "flex items-center gap-2"}
    (if (fn? icon)
      [icon]
      [:> icon {:size 16 :class "text-gray-11"}])
    [:div {:class "flex justify-between items-center w-full"}
     [:span {:class (str "text-sm font-medium"
                         (when requires-upgrade? " text-opacity-70"))}
      label]
     (when requires-upgrade?
       [:span {:class "text-xs text-blue-9 font-medium"} "Upgrade"])]]])

(defn connection-result-item
  "Connection search result item"
  [connection]
  [:> CommandItem
   {:key (:id connection)
    :value (str "role:" (:name connection))
    :keywords [(:type connection) (:subtype connection) (:status connection) "connection"]
    :onSelect #(do
                 (rf/dispatch [:database-schema->clear-schema])
                 (rf/dispatch [:command-palette->navigate-to-page :connection-actions connection]))}
   [:div {:class "flex items-center gap-2"}
    [:> Rotate3d {:size 16 :class "text-gray-11"}]
    [:div {:class "flex flex-col"}
     [:span {:class "text-sm font-medium"}
      (:name connection)]]
    [:> ChevronRight {:size 16 :class "ml-auto text-gray-9"}]]])

(defn resource-result-item
  "Resource search result item"
  [resource]
  [:> CommandItem
   {:key (:name resource)
    :value (str "resource:" (:name resource))
    :keywords [(:name resource) "resource"]
    :onSelect #(rf/dispatch [:command-palette->navigate-to-page :resource-roles resource])}
   [:div {:class "flex items-center gap-2"}
    [:> Package {:size 16 :class "text-gray-11"}]
    [:div {:class "flex flex-col"}
     [:span {:class "text-sm font-medium"}
      (:name resource)]]
    [:> ChevronRight {:size 16 :class "ml-auto text-gray-9"}]]])

(defn runbook-result-item
  "Runbook search result item"
  [runbook]
  (let [runbook-name (:name runbook)
        repository (:repository runbook)
        repo-name (runbooks-helpers/extract-repo-name repository)
        [folder filename] (if (re-find #"/" runbook-name)
                            (let [parts (cs/split runbook-name #"/" 2)]
                              [(str (first parts) "/") (second parts)])
                            [nil runbook-name])
        template {:name runbook-name}]
    [:> CommandItem
     {:key (str repository ":" runbook-name)
      :value (str repository ":" runbook-name)
      :keywords (filterv some? [repository runbook-name "runbook"])
      :onSelect #(do
                   (rf/dispatch [:runbooks/set-active-runbook template repository])
                   (rf/dispatch [:navigate :runbooks])
                   (rf/dispatch [:command-palette->close]))}
     [:div {:class "flex items-center gap-2"}
      [:> File {:size 16 :class "text-gray-9"}]
      [:div {:class "flex flex-1 min-w-0 items-center gap-3"}
       (when repository
         [:span {:class "text-sm font-medium text-gray-9"} "@" repo-name])
       (when folder
         [:span {:class "text-sm font-medium text-gray-9"} folder])
       [:span {:class "text-sm font-medium truncate"} filename]]
      [:> ChevronRight {:size 16 :class "ml-auto text-gray-9 flex-shrink-0"}]]]))

(defn main-page
  "Main page with all pages + search functionality"
  [search-results user-data]
  (let [search-status (:status search-results)
        resources (:resources (:data search-results))
        connections (:connections (:data search-results))
        runbooks (:runbooks (:data search-results))
        ;; Filter items based on user permissions
        filtered-items (constants/filter-items-by-permissions user-data)
        ;; Separate items into groups
        suggestions (filter #(contains? #{"Resources" "Terminal"} (:id %)) filtered-items)
        quick-access (remove #(contains? #{"Resources" "Terminal"} (:id %)) filtered-items)]
    [:<>
     ;; Search results (if any)
     (when (and (= search-status :ready) (or (seq resources) (seq connections) (seq runbooks)))
       [:<>
        (when (seq resources)
          [:> CommandGroup
           {:heading "Resources"}
           (for [resource resources]
             ^{:key (:name resource)}
             [resource-result-item resource])])

        (when (seq connections)
          [:> CommandGroup
           {:heading "Roles"}
           (for [connection connections]
             ^{:key (:id connection)}
             [connection-result-item connection])])

        (when (seq runbooks)
          [:> CommandGroup
           {:heading "Runbooks"}
           (for [runbook runbooks]
             ^{:key (str (:repository runbook) ":" (:name runbook))}
             [runbook-result-item runbook])])

        [:> CommandSeparator]])

     ;; Suggestions (Resources and Terminal)
     [:> CommandGroup
      {:heading "Suggestions"}
      (for [item suggestions]
        ^{:key (:id item)}
        [action-item item])]

     [:> CommandSeparator]

     ;; Quick Access (rest of the pages)
     [:> CommandGroup
      {:heading "Quick Access"}
      (for [item quick-access]
        ^{:key (:id item)}
        [action-item item])]]))

(defn role-item
  "Role item in resource-roles page"
  [role]
  [:> CommandItem
   {:key (:id role)
    :value (:name role)
    :keywords [(:type role) (:subtype role) (:status role) "role"]
    :onSelect #(do
                 (rf/dispatch [:database-schema->clear-schema])
                 (rf/dispatch [:command-palette->navigate-to-page :connection-actions role]))}
   [:div {:class "flex items-center gap-2"}
    [:> Rotate3d {:size 16 :class "text-gray-11"}]
    [:div {:class "flex flex-col"}
     [:span {:class "text-sm font-medium"}
      (:name role)]]
    [:> ChevronRight {:size 16 :class "ml-auto text-gray-9"}]]])

(defn resource-roles-page
  "Resource roles page - shows all roles (connections) for a specific resource"
  [resource _user-data]
  (let [resource-name (:name resource)
        resource-roles (rf/subscribe [:resources->resource-roles resource-name])
        roles-data (:data @resource-roles)
        loading? (:loading @resource-roles)]
    [:<>
     ;; Show loading state with timeout to avoid flashing
     (when (and loading? (empty? roles-data))
       [:> CommandGroup
        [:div {:class "flex items-center justify-center py-4 text-sm text-gray-11"}
         "Loading roles..."]])

     ;; Show roles when loaded
     (when (and (not loading?) (seq roles-data))
       [:> CommandGroup
        (for [role roles-data]
          ^{:key (:id role)}
          [role-item role])])

     ;; Empty state
     (when (and (not loading?) (empty? roles-data))
       [:> CommandGroup
        [:div {:class "flex items-center justify-center py-4 text-sm text-gray-11"}
         "No roles found in this resource"]])]))

(defn connection-actions-page
  "Connection-specific actions page"
  [connection user-data]
  (let [is-admin? (:admin? user-data)
        ;; Get actions dynamically based on connection permissions
        all-actions (constants/get-connection-actions connection is-admin?)
        ;; Separate main actions from configuration
        main-actions (remove #(= (:id %) "configure") all-actions)
        ;; Configuration actions (already filtered by admin in get-connection-actions)
        config-actions (filter #(= (:id %) "configure") all-actions)]
    [:<>
     ;; Main actions
     (when (seq main-actions)
       [:> CommandGroup
        (for [action main-actions]
          ^{:key (:id action)}
          [action-item (assoc action
                              :connection-name (:name connection)
                              :connection-id (:id connection))])])

     ;; Separator if both groups exist
     (when (and (seq main-actions) (seq config-actions))
       [:> CommandSeparator])

     ;; Configuration actions
     (when (seq config-actions)
       [:> CommandGroup
        {:heading "Settings"}
        (for [action config-actions]
          ^{:key (:id action)}
          [action-item (assoc action
                              :connection-name (:name connection)
                              :connection-id (:id connection))])])]))
