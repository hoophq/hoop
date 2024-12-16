(ns webapp.connections.views.connection-list
  (:require ["lucide-react" :refer [Wifi Tags EllipsisVertical]]
            ["@radix-ui/themes" :refer [IconButton DropdownMenu Tooltip
                                        Flex Text Badge]]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.loaders :as loaders]
            [webapp.components.searchbox :as searchbox]
            [webapp.connections.constants :as connection-constants]
            [webapp.config :as config]
            [webapp.connections.views.create-update-connection.main :as create-update-connection]))

(defn empty-list-view []
  [:div {:class "pt-x-large"}
   [:figure
    {:class "w-1/6 mx-auto p-regular"}
    [:img {:src (str config/webapp-url "/images/illustrations/pc.svg")
           :class "w-full"}]]
   [:div {:class "px-large text-center"}
    [:div {:class "text-gray-700 text-sm font-bold"}
     "Beep boop, no sessions to look"]
    [:div {:class "text-gray-500 text-xs mb-large"}
     "There's nothing with this criteria"]]])


(defn- loading-list-view []
  [:div {:class "flex items-center justify-center rounded-lg border bg-white h-full"}
   [:div {:class "flex items-center justify-center h-full"}
    [loaders/simple-loader]]])

(defn panel [_]
  (let [search-string (.. js/window -location -search)
        url-params (new js/URLSearchParams search-string)
        tags (.get url-params "tags")
        connections (rf/subscribe [:connections])
        user (rf/subscribe [:users->current-user])
        query (r/atom {:tags (if (and tags
                                      (not (cs/blank? tags)))
                               (cs/split tags #",")
                               [])})
        search-focused (r/atom false)
        searched-connections (r/atom nil)
        searched-criteria-connections (r/atom "")
        connections-search-status (r/atom nil)]
    (rf/dispatch [:connections->get-connections])
    (rf/dispatch [:users->get-user])
    (rf/dispatch [:guardrails->get-all])
    (fn []
      (let [connections-search-results (cond->> (if (empty? @searched-connections)
                                                  (:results @connections)
                                                  @searched-connections)
                                         (seq (:tags @query))
                                         (filter (fn [connection]
                                                   (some (set (:tags @query))
                                                         (:tags connection)))))
            connections-tags (doall (->> (:results @connections)
                                         (map :tags)
                                         (apply concat)
                                 (distinct)))]
        [:div {:class "flex flex-col bg-white rounded-lg h-full p-6 overflow-y-auto"}
         (when (-> @user :data :admin?)
           [:div {:class "absolute top-10 right-4 sm:right-6 lg:top-16 lg:right-20"}
            [button/tailwind-primary {:text "Add connection"
                                      :on-click (fn []
                                                  (rf/dispatch [:navigate :create-connection]))}]])
         [:> Flex {:as "header"
                   :direction "column"
                   :gap "3"
                   :class "mb-4"}
          [searchbox/main
           {:options (:results @connections)
            :display-key :name
            :searchable-keys [:name :type :subtype :tags :status]
            :on-change-results-cb #(reset! searched-connections %)
            :hide-results-list true
            :placeholder "Search by connection name, type, status or anything"
            :on-focus #(reset! search-focused true)
            :on-blur #(reset! search-focused false)
            :name "connection-search"
            :on-change #(reset! searched-criteria-connections %)
            :loading? (= @connections-search-status :loading)
            :size :small
            :icon-position "left"}]
          (when (not-empty connections-tags)
             [:> Flex {:gap "4"
                       :align "center"}
              [:> Text {:size "1"
                        :color :gray
                        :weight "bold"}
               "Tags"]
              [:> Flex {:gap "2"
                        :wrap "wrap"
                        :justify "between"
                        :position "relative"}
               (doall
                 (for [tag connections-tags]
                   [:> Badge {:variant (if (some #{tag} (get @query :tags)) "solid" "soft")
                              :as "div"
                              :on-click #(do
                                           (if (not (some #{tag} (get @query :tags)))
                                             (reset! query
                                                     {:tags (concat (get @query :tags)
                                                                    [tag])})
                                             (reset! query
                                                     {:tags (remove #{tag} (get @query :tags))}))
                                           (rf/dispatch [:connections->filter-connections @query]))
                              :key tag
                              :radius "full"
                              :class "cursor-pointer"}
                    tag]))]])]

         (if (and (= :loading (:status @connections)) (empty? (:results @connections)))
           [loading-list-view]

           [:div {:class "rounded-lg border bg-white h-full overflow-y-auto"}
            [:div {:class "relative h-full overflow-y-auto"}
                ;;  (when (and (= status :loading) (empty? (:data sessions)))
                ;;    [loading-list-view])
             (when (and (empty? (:results  @connections)) (not= (:status @connections) :loading))
               [empty-list-view])

             (if (and (empty? @searched-connections)
                      (> (count @searched-criteria-connections) 0))
               [:div {:class "px-regular py-large text-xs text-gray-700 italic"}
                "No connections with this criteria"]

               (doall
                (for [connection connections-search-results]
                  ^{:key (:id connection)}
                  [:div {:class (str "border-b last:border-0 hover:bg-gray-50 text-gray-700 "
                                     " p-regular text-xs flex gap-8 justify-between items-center")}
                   [:div {:class "flex truncate items-center gap-regular"}
                    [:div
                     [:figure {:class "w-5"}
                      [:img {:src  (connection-constants/get-connection-icon connection)
                             :class "w-9"}]]]
                    [:span {:class "block truncate"}
                     (:name connection)]]
                   [:div {:id "connection-info"
                          :class "flex gap-6 items-center"}

                    (when (seq (:tags connection))
                      [:div {:class "relative group flex items-center gap-2 text-xs text-gray-700"}
                       [:div
                        [:> Tags {:size 16}]]
                       [:span {:class "text-nowrap font-semibold"}
                        (str (first (:tags connection))
                             (when (> (count (:tags connection)) 1)
                               (str " + " (- (count (:tags connection)) 1) " more")))]])

                    [:div {:class "flex items-center gap-1 text-xs text-gray-700"}
                     [:div {:class (str "rounded-full h-[6px] w-[6px] "
                                        (if (= (:status connection) "online")
                                          "bg-green-500"
                                          "bg-red-500"))}]
                     (cs/capitalize (:status connection))]

                    (when (or
                           (= "database" (:type connection))
                           (and (= "application" (:type connection))
                                (= "tcp" (:subtype connection))))
                      [:div {:class "relative cursor-pointer group"
                             :on-click #(rf/dispatch [:connections->start-connect (:name connection)])}
                       [:> Tooltip {:content "Hoop Access"}
                        [:> IconButton {:size 1 :variant "ghost" :color "gray"}
                         [:> Wifi {:size 16}]]]])

                    [:> DropdownMenu.Root {:dir "rtl"}
                     [:> DropdownMenu.Trigger
                      [:> IconButton {:size 1 :variant "ghost" :color "gray"}
                       [:> EllipsisVertical {:size 16}]]]
                     [:> DropdownMenu.Content
                      (when (and (-> @user :data :admin?)
                                 (not (= (:managed_by connection) "hoopagent")))
                        [:> DropdownMenu.Item {:on-click (fn []
                                                           (rf/dispatch [:plugins->get-my-plugins])
                                                           (rf/dispatch [:connections->get-connection {:connection-name (:name connection)}])
                                                           (rf/dispatch [:modal->open {:content [create-update-connection/main :update connection]}]))}
                         "Configure"])
                      [:> DropdownMenu.Item {:color "red"
                                             :on-click (fn []
                                                         (rf/dispatch [:connections->delete-connection (:name connection)]))}
                       "Delete"]]]]])))]])]))))
