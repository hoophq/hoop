(ns webapp.features.access-control.views.group-list
  (:require
   ["@headlessui/react" :as ui]
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Text TextField]]
   ["lucide-react" :refer [ArrowRightLeft Check ChevronDown ChevronUp]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [clojure.string :as cs]
   [webapp.connections.constants :as connection-constants]
   [webapp.components.infinite-scroll :refer [infinite-scroll]]))

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

(defn connection-filter-popover [{:keys [selected-connection search-term search-debounce-timer connections-pagination connections-data connections-loading? has-more? current-page]}]
  [:<>
   [:> ui/Popover.Button {:class (str (if (seq @selected-connection)
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
    (when (seq @selected-connection)
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
     (when (seq @selected-connection)
       [:div {:class "mb-2 pb-2 border-b border-gray-200"}
        [:div {:class (str "flex cursor-pointer items-center gap-2 "
                           "text-sm text-gray-700 hover:bg-gray-200 rounded-md px-3 py-2")
               :on-click (fn []
                           (reset! selected-connection "")
                           (rf/dispatch [:ui/close-popover]))}
         [:span "Clear filter"]]])

     [:div {:class "mb-2"}
      [:> TextField.Root {:class "w-full"
                          :placeholder "Search connections"
                          :value @search-term
                          :onChange (fn [e]
                                      (let [value (-> e .-target .-value)
                                            trimmed (cs/trim value)
                                            should-search? (or (cs/blank? trimmed)
                                                               (> (count trimmed) 2))
                                            request (cond-> {:page 1 :force-refresh? true}
                                                      (seq trimmed) (assoc :search trimmed))]
                                        (reset! search-term value)
                                        (when @search-debounce-timer
                                          (js/clearTimeout @search-debounce-timer))
                                        (if should-search?
                                          (reset! search-debounce-timer
                                                  (js/setTimeout
                                                   (fn []
                                                     (rf/dispatch [:connections/get-connections-paginated request]))
                                                   300))
                                          (reset! search-debounce-timer nil))))}]]

     (if (> (count connections-data) 0)
       [:div {:class "relative"}
        [infinite-scroll
         {:on-load-more (fn []
                          (when (not connections-loading?)
                            (let [next-page (inc current-page)
                                  active-search (:active-search @connections-pagination)
                                  next-request (cond-> {:page next-page
                                                        :force-refresh? false}
                                                 (not (cs/blank? active-search)) (assoc :search active-search))]
                              (rf/dispatch [:connections/get-connections-paginated next-request]))))
          :has-more? has-more?
          :loading? connections-loading?}
         [:ul
          (doall
           (for [connection connections-data]
             ^{:key (:name connection)}
             [:li {:class (str "flex justify-between cursor-pointer items-center gap-2 "
                               "text-sm text-gray-700 hover:bg-gray-200 rounded-md px-3 py-2")
                   :on-click (fn []
                               (reset! selected-connection (:name connection))
                               (rf/dispatch [:ui/close-popover]))}
              [:div {:class "w-full flex justify-between items-center gap-3"}
               [:div {:class "flex items-center gap-2"}
                [:figure {:class "w-4"}
                 [:img {:src (connection-constants/get-connection-icon connection)
                        :class "w-full"}]]
                [:span {:class "block truncate"}
                 (:name connection)]]
               (when (= (:name connection) @selected-connection)
                 [:> Check {:size 16}])]]))]]]
       [:div {:class "px-3 py-4 text-xs text-gray-700 italic"}
        (if (seq @search-term)
          "No connections found matching your search"
          "No connections with this criteria")])]]])

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
  (let [all-groups (rf/subscribe [:access-control/all-groups])
        groups-with-permissions (rf/subscribe [:access-control/groups-with-permissions])
        connections-pagination (rf/subscribe [:connections->pagination])

        selected-connection (r/atom "")
        search-term (r/atom "")
        search-debounce-timer (r/atom nil)]

    (rf/dispatch [:connections/get-connections-paginated {:page 1 :force-refresh? true}])
    (println @search-term)
    (fn []
      (let [filtered-groups (or @all-groups [])
            group-permissions (or @groups-with-permissions {})
            connections-data (or (:data @connections-pagination) [])
            connections-loading? (:loading @connections-pagination)
            has-more? (:has-more? @connections-pagination)
            current-page (:current-page @connections-pagination 1)
            connections-map (reduce #(assoc %1 (:name %2) %2) {} connections-data)

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
                                  (sort-by :name))]

        [:<>
         ;; Connection Filter
         [:> Box {:class "flex mb-6"}
          [:> ui/Popover {:class "relative"}
           [connection-filter-popover {:selected-connection selected-connection
                                       :search-term search-term
                                       :search-debounce-timer search-debounce-timer
                                       :connections-pagination connections-pagination
                                       :connections-data connections-data
                                       :connections-loading? connections-loading?
                                       :has-more? has-more?
                                       :current-page current-page}]]]

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
