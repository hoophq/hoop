(ns webapp.features.access-control.views.group-list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Text]]
   ["lucide-react" :refer [ChevronDown ChevronUp]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.connections.constants :as connection-constants]
   [webapp.components.connection-filter :refer [connection-filter]]))

(defn- get-group-connections [group-name groups-with-permissions]
  (get groups-with-permissions group-name []))

(defn- connections-panel [{:keys [connections]}]
  [:> Box {:px "7" :py "5" :class "border-t rounded-b-6 bg-white"}
   [:> Grid {:columns "7" :gap "7"}
    [:> Box {:grid-column "span 2 / span 2"}
     [:> Heading {:as "h4" :size "4" :weight "medium" :class "text-[--gray-12]"}
      "Resources"]
     [:> Text {:size "3" :class "text-[--gray-11]"}
      "These resource roles can be accessed by this user group."]]

    [:> Box {:class "h-fit border border-[--gray-a6] rounded-md" :grid-column "span 5 / span 5"}
     (if (empty? connections)
       [:> Flex {:p "4" :justify "center" :align "center"}
        [:> Text {:size "2" :class "text-[--gray-11] italic"}
         "No resource roles assigned to this group"]]

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
            "Resource Roles"
            (if @show-connections?
              [:> ChevronUp {:size 14}]
              [:> ChevronDown {:size 14}])])]]

       (when @show-connections?
         [connections-panel {:connections connections}])])))

(defn main []
  (let [all-groups (rf/subscribe [:access-control/all-groups])
        groups-with-permissions (rf/subscribe [:access-control/groups-with-permissions])
        connections (rf/subscribe [:connections->pagination])

        selected-connection (r/atom nil)]

    (fn []
      (let [filtered-groups (or @all-groups [])
            group-permissions (or @groups-with-permissions {})
            connections-data (or (:data @connections) [])
            connections-map (reduce #(assoc %1 (:name %2) %2) {} connections-data)

            groups-filtered-by-connection (if (nil? @selected-connection)
                                            filtered-groups
                                            (filter (fn [group-name]
                                                      (let [group-connections (get-group-connections group-name group-permissions)]
                                                        (some #(= (:name %) @selected-connection) group-connections)))
                                                    filtered-groups))

            processed-groups (->> groups-filtered-by-connection
                                  (map (fn [group-name]
                                         (let [group-connection-ids (get-group-connections group-name group-permissions)
                                               group-connections (map (fn [conn]
                                                                        (or (get connections-map (:name conn)) conn))
                                                                      group-connection-ids)]
                                           {:name group-name
                                            :active? (contains? group-permissions group-name)
                                            :connections group-connections})))
                                  (sort-by :name))]

        [:<>
         [:> Box {:mb "6"}
          [connection-filter {:selected @selected-connection
                              :on-select #(reset! selected-connection %)
                              :on-clear #(reset! selected-connection nil)
                              :label "Resource Roles"}]]

         [:> Box
          (if (empty? processed-groups)
            [:> Flex {:direction "column" :justify "center" :align "center" :class "h-40"}
             [:> Text {:size "3" :class "text-[--gray-11] text-center"}
              (if (nil? @selected-connection)
                "No groups found"
                (str "No groups have access to \"" @selected-connection "\""))]]

            (doall
             (for [group processed-groups]
               ^{:key (:name group)}
               [group-item (assoc group :total-items (count processed-groups))])))]]))))
