(ns webapp.features.access-control.views.group-list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Text]]
   ["lucide-react" :refer [ChevronDown ChevronUp]]
   [re-frame.core :as rf]
   [reagent.core :as r]
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

(defn group-item [{:keys [name connections total-items]}]
  (let [show-connections? (r/atom false)]
    (fn []
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
        all-connections (rf/subscribe [:connections])]

    ;; Fetch all connections when component mounts
    (rf/dispatch [:connections->get-connections])

    (fn []
      (let [all-groups (or @user-groups [])
            group-permissions (or @groups-with-permissions {})
            connections-map (reduce #(assoc %1 (:name %2) %2) {} (:results @all-connections))
            filtered-groups (filter #(not= "admin" %) all-groups)
            processed-groups (->> filtered-groups
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
        [:> Box {:class "w-full h-full"}
         [:> Box
          (doall
           (for [group processed-groups]
             ^{:key (:name group)}
             [group-item (assoc group :total-items (count processed-groups))]))]]))))
