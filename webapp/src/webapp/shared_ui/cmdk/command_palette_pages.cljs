(ns webapp.shared-ui.cmdk.command-palette-pages
  (:require
   ["cmdk" :refer [CommandGroup CommandItem CommandSeparator]]
   ["lucide-react" :refer [File ChevronRight]] 
   [clojure.string :as cs]
   [re-frame.core :as rf]
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
    :value (:name connection)
    :keywords [(:type connection) (:subtype connection) (:status connection) "connection"]
    :onSelect #(rf/dispatch [:command-palette->navigate-to-page :connection-actions connection])}
   [:div {:class "flex items-center gap-2"}
    [:div {:class "flex flex-col"}
     [:span {:class "text-sm font-medium"} (:name connection)]]]])

(defn runbook-result-item
  "Runbook search result item"
  [runbook-path]
  (let [[folder filename] (if (re-find #"/" runbook-path)
                            (let [parts (cs/split runbook-path #"/" 2)]
                              [(str (first parts) "/") (second parts)])
                            [nil runbook-path])]
    [:> CommandItem
     {:key (:id runbook-path)
      :value runbook-path
      :keywords (filterv some? [(:category runbook-path) (:tags runbook-path) "runbook"])
      :onSelect #(do
                   (rf/dispatch [:runbooks-plugin->set-active-runbook-by-name runbook-path])
                   (rf/dispatch [:navigate :runbooks])
                   (rf/dispatch [:command-palette->close]))}
     [:div {:class "flex items-center gap-2"}
      [:> File {:size 16 :class "text-gray-9"}]
      [:div {:class "flex flex-col"}
       [:span {:class "text-sm font-medium"}
        (when folder
          [:span {:class "text-gray-9"} folder " "])
        filename]]
      [:> ChevronRight {:size 16 :class "ml-auto text-gray-9"}]]]))

(defn main-page
  "Main page with all pages + search functionality"
  [search-results user-data]
  (let [search-status (:status search-results)
        connections (:connections (:data search-results))
        runbooks (:runbooks (:data search-results))
        ;; Filter items based on user permissions
        filtered-items (constants/filter-items-by-permissions user-data)
        ;; Separate items into groups
        suggestions (filter #(contains? #{"Connections" "Terminal"} (:id %)) filtered-items)
        quick-access (remove #(contains? #{"Connections" "Terminal"} (:id %)) filtered-items)]
    [:<>
     ;; Search results (if any)
     (when (and (= search-status :ready) (or (seq connections) (seq runbooks)))
       [:<>
        (when (seq connections)
          [:> CommandGroup
           {:heading "Connections"}
           (for [connection connections]
             ^{:key (:id connection)}
             [connection-result-item connection])])

        (when (seq runbooks)
          [:> CommandGroup
           {:heading "Runbooks"}
           (for [runbook runbooks]
             ^{:key runbook}
             [runbook-result-item runbook])])

        [:> CommandSeparator]])

     ;; Suggestions (Connections and Terminal)
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
