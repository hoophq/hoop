(ns webapp.features.access-control.views.group-list
  (:require
   ["@headlessui/react" :as ui]
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Text]]
   ["lucide-react" :refer [ArrowRightLeft Check ChevronDown ChevronUp]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.searchbox :as searchbox]
   [webapp.connections.constants :as connection-constants]))

(defn- get-group-connections [group-name groups-with-permissions]
  (get groups-with-permissions group-name []))

(defn- connections-panel [{:keys [connections]}]
  [:> Box {:px "7" :py "5" :class "border-t rounded-b-6 bg-white"}
   [:> Grid {:columns "7" :gap "7"}
    [:> Box {:grid-column "span 2 / span 2"}
     [:> Heading {:as "h4" :size "4" :weight "medium" :class "text-[--gray-12]"}
      "Connections List"]
     [:> Text {:size "3" :class "text-[--gray-11]"}
      "These connections can be accessed by this user group."]]

    [:> Box {:class "h-fit border border-[--gray-a6] rounded-md" :grid-column "span 5 / span 5"}
     (if (empty? connections)
       [:> Flex {:p "4" :justify "center" :align "center"}
        [:> Text {:size "2" :class "text-[--gray-11] italic"}
         "No connections assigned to this group"]]

       (for [connection connections]
         ^{:key (:name connection)}
         [:> Flex {:p "2" :align "center" :justify "between" :class "last:border-b-0 border-b border-[--gray-a6]"}
          [:> Flex {:gap "2" :align "center"}
           [:> Box
            [:figure {:class "w-4"}
             [:img {:src (or (connection-constants/get-connection-icon connection) "/icons/database.svg")
                    :class "w-9"}]]]
           [:span {:class "text-sm"} (:name connection)]]
          [:> Button {:size "1"
                      :variant "soft"
                      :color "gray"
                      :on-click (fn []
                                  (rf/dispatch [:plugins->get-my-plugins])
                                  (rf/dispatch [:connections->get-connection {:connection-name (:name connection)}])
                                  (rf/dispatch [:navigate :edit-connection {} :connection-name (:name connection)]))}
           "Configure"]]))]]])

(defn group-item []
  (let [show-connections? (r/atom false)]
    (fn [{:keys [name connections total-items]}]
      [:> Box {:class (str "first:rounded-t-6 last:rounded-b-6 data-[state=open]:bg-[--accent-2] "
                           "border-[--gray-a6] border "
                           (when (> total-items 1) " first:border-b-0")
                           (when @show-connections? " bg-[--accent-2]"))}
       [:> Box {:p "5" :class "flex justify-between items-center"}
        [:> Flex {:direction "column"}
         [:> Heading {:as "h3" :size "5" :weight "medium" :class "text-[--gray-12]"}
          name]]

        [:> Flex {:align "center" :gap "4"}
         [:> Button {:size "3"
                     :variant "soft"
                     :color "gray"
                     :on-click (fn []
                                 (rf/dispatch [:navigate :access-control-edit {:group name}]))}
          "Configure"]

         (when-not (empty? connections)
           [:> Button {:size "1"
                       :variant "ghost"
                       :color "gray"
                       :on-click #(swap! show-connections? not)}
            "Connections"
            (if @show-connections?
              [:> ChevronUp {:size 14}]
              [:> ChevronDown {:size 14}])])]]

       (when @show-connections?
         [connections-panel {:connections connections}])])))

(defn main []
  (let [user-groups (rf/subscribe [:user-groups])
        groups-with-permissions (rf/subscribe [:access-control/groups-with-permissions])
        all-connections (rf/subscribe [:connections])

        ;; Estado do filtro de conexão
        selected-connection (r/atom "")
        searched-connections (r/atom nil)
        searched-criteria-connections (r/atom "")]

    ;; Fetch all connections when component mounts
    (rf/dispatch [:connections->get-connections])

    (fn []
      (let [all-groups (or @user-groups [])
            group-permissions (or @groups-with-permissions {})
            connections-map (reduce #(assoc %1 (:name %2) %2) {} (:results @all-connections))
            filtered-groups (filter #(not= "admin" %) all-groups)

            ;; Aplicar filtro de conexão se selecionado
            groups-filtered-by-connection (if (empty? @selected-connection)
                                            filtered-groups
                                            (filter (fn [group-name]
                                                      (let [group-connections (get-group-connections group-name group-permissions)]
                                                        (some #(= (:name %) @selected-connection) group-connections)))
                                                    filtered-groups))

            processed-groups (->> groups-filtered-by-connection
                                  (map (fn [group-name]
                                         (let [group-connection-ids (get-group-connections group-name group-permissions)
                                               ;; Map connection IDs to full connection objects
                                               group-connections (map (fn [conn]
                                                                        (or (get connections-map (:name conn)) conn))
                                                                      group-connection-ids)]
                                           {:name group-name
                                            :active? (contains? group-permissions group-name)
                                            :connections group-connections})))
                                  (sort-by :name))

            connections-search-results (if (empty? @searched-connections)
                                         (:results @all-connections)
                                         @searched-connections)]

        [:<>

         ;; Connection Filter
         [:> Box {:class "flex mb-6"}
          [:> ui/Popover {:class "relative"}
           (fn [params]
             (r/as-element
              [:<>
               [:> ui/Popover.Button {:class (str (if (not (empty? @selected-connection))
                                                    "bg-gray-50 text-gray-600 border-gray-400 "
                                                    "text-gray-500 border-gray-300 ")
                                                  "w-full max-w-sm flex gap-2 items-center cursor-pointer "
                                                  "border rounded-md px-3 py-2 "
                                                  "hover:bg-gray-50 hover:text-gray-600 hover:border-gray-400")}
                [:> ArrowRightLeft {:size 16}]
                [:span {:class "text-sm font-semibold"}
                 (if (empty? @selected-connection)
                   "Connection"
                   @selected-connection)]
                (when (not (empty? @selected-connection))
                  [:div {:class "flex items-center justify-center rounded-full h-4 w-4 bg-gray-800"}
                   [:span {:class "text-white text-xs font-bold"}
                    "1"]])]

               [:> ui/Popover.Panel {:class (str "absolute mt-2 z-10 w-96 max-h-96 "
                                                 "overflow-y-auto bg-white border border-gray-300 "
                                                 "rounded-lg shadow-lg p-4")}
                [:div {:class (str "absolute w-2 h-2 "
                                   "left-4 -top-1 border-gray-300 "
                                   "bg-white border-t border-l "
                                   "rounded transform rotate-45")}]
                [:div
                 ;; Clear filter option
                 (when (not (empty? @selected-connection))
                   [:div {:class "mb-2 pb-2 border-b border-gray-200"}
                    [:div {:class (str "flex cursor-pointer items-center gap-2 "
                                       "text-sm text-gray-700 hover:bg-gray-200 rounded-md px-3 py-2")
                           :on-click (fn []
                                       (reset! selected-connection "")
                                       (.close params))}
                     [:span "Clear filter"]]])

                 [:div {:class "mb-2"}
                  [searchbox/main
                   {:options (:results @all-connections)
                    :display-key :name
                    :variant :small
                    :searchable-keys [:name :type :tags]
                    :on-change-results-cb #(reset! searched-connections %)
                    :hide-results-list true
                    :placeholder "Search connections"
                    :name "connection-search"
                    :on-change #(reset! searched-criteria-connections %)
                    :loading? (empty? (:results @all-connections))
                    :size :small}]]

                 (if (and (empty? @searched-connections)
                          (> (count @searched-criteria-connections) 0))
                   [:div {:class "px-3 py-4 text-xs text-gray-700 italic"}
                    "No connections with this criteria"]

                   [:div {:class "relative"}
                    [:ul
                     (doall
                      (for [connection connections-search-results]
                        ^{:key (:name connection)}
                        [:li {:class (str "flex justify-between cursor-pointer items-center gap-2 "
                                          "text-sm text-gray-700 hover:bg-gray-200 rounded-md px-3 py-2")
                              :on-click (fn []
                                          (reset! selected-connection (:name connection))
                                          (.close params))}
                         [:div {:class "w-full flex justify-between items-center gap-3"}
                          [:div {:class "flex items-center gap-2"}
                           [:figure {:class "w-4"}
                            [:img {:src (connection-constants/get-connection-icon connection)
                                   :class "w-full"}]]
                           [:span {:class "block truncate"}
                            (:name connection)]]
                          (when (= (:name connection) @selected-connection)
                            [:> Check {:size 16}])]]))]])]]]))]]

         [:> Box
          (if (empty? processed-groups)
            ;; Empty state quando não há grupos que correspondem ao filtro
            [:> Flex {:direction "column" :justify "center" :align "center" :class "h-40"}
             [:> Text {:size "3" :class "text-gray-500 text-center"}
              (if (empty? @selected-connection)
                "No groups found"
                (str "No groups have access to \"" @selected-connection "\""))]]

            ;; Lista de grupos filtrados
            (doall
             (for [group processed-groups]
               ^{:key (:name group)}
               [group-item (assoc group :total-items (count processed-groups))])))]]))))
