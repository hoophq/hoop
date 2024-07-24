(ns webapp.connections.views.connection-list
  (:require ["@headlessui/react" :as ui]
            ["@heroicons/react/16/solid" :as hero-micro-icon]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.loaders :as loaders]
            [webapp.components.searchbox :as searchbox]
            [webapp.connections.constants :as connection-constants]
            [webapp.connections.views.connection-connect :as connection-connect]
            [webapp.connections.views.connection-form-modal :as connection-form-modal]))


(defn- connections-filters []
  (let [connections (rf/subscribe [:connections])
        search-focused (r/atom false)
        searched-connections (r/atom nil)
        searched-criteria-connections (r/atom "")
        connections-search-status (r/atom nil)

        connection-types-options [{:text "Custom" :value "custom"}
                                  {:text "Database" :value "database"}
                                  {:text "Application" :value "application"}]
        searched-connections-types (r/atom nil)
        searched-criteria-connections-types (r/atom "")]
    (fn [filters]
      (let [connections-search-results (if (empty? @searched-connections)
                                         (:results @connections)
                                         @searched-connections)
            connection-types-search-options (if (empty? @searched-connections-types)
                                              connection-types-options
                                              @searched-connections-types)]
        [:div {:class "flex gap-regular flex-wrap mb-4"}
         [:> ui/Popover {:class "relative"}
          (fn [params]
            (r/as-element
             [:<>
              [:> ui/Popover.Button {:class (str (if (get filters "connection")
                                                   "bg-gray-50 text-gray-600 border-gray-400 "
                                                   "text-gray-500 border-gray-300 ")
                                                 "w-full flex gap-small items-center cursor-pointer "
                                                 "border rounded-md px-3 py-2 "
                                                 "hover:bg-gray-50 hover:text-gray-600 hover:border-gray-400")}
               [:> hero-micro-icon/ArrowsRightLeftIcon {:class "w-4 h-4"}]
               [:span {:class "text-sm font-semibold"}
                "Connection"]
               (when (get filters "connection")
                 [:div {:class "flex items-center justify-center rounded-full h-4 w-4 bg-gray-800"}
                  [:span {:class "text-white text-xxs font-bold"}
                   "1"]])]
              [:> ui/Popover.Panel {:class (str "absolute mt-2 z-10 w-96 max-h-96 "
                                                "overflow-y-auto bg-white border border-gray-300 "
                                                "rounded-lg shadow-lg p-4")}
               [:div {:class (str "absolute w-2 h-2 "
                                  "left-4 -top-1 border-gray-300 "
                                  "bg-white border-t border-l "
                                  "rounded transform rotate-45")}]
               [:div
                [:div {:class "mb-2"}
                 [searchbox/main
                  {:options (:results @connections)
                   :display-key :name
                   :variant :small
                   :searchable-keys [:name :type :tags]
                   :on-change-results-cb #(reset! searched-connections %)
                   :hide-results-list true
                   :placeholder "Search"
                   :on-focus #(reset! search-focused true)
                   :on-blur #(reset! search-focused false)
                   :name "connection-search"
                   :on-change #(reset! searched-criteria-connections %)
                   :loading? (= @connections-search-status :loading)
                   :size :small}]]

                (if (and (empty? @searched-connections)
                         (> (count @searched-criteria-connections) 0))
                  [:div {:class "px-regular py-large text-xs text-gray-700 italic"}
                   "No connections with this criteria"]

                  [:div {:class "relative"}
                   [:ul
                    (for [connection connections-search-results]
                      ^{:key (:name connection)}
                      [:li {:class (str "flex justify-between cursor-pointer items-center gap-small "
                                        "text-sm text-gray-700 hover:bg-gray-200 rounded-md px-3 py-2")
                            :on-click (fn []
                                        (rf/dispatch [:audit->filter-sessions
                                                      {"connection" (if (= (:name connection) (get filters "connection"))
                                                                      ""
                                                                      (:name connection))}])
                                        (.close params))}
                       [:div {:class "w-full flex justify-between items-center gap-regular"}
                        [:div {:class "flex items-center gap-small"}
                         [:figure {:class "w-5"}
                          [:img {:src  (connection-constants/get-connection-icon connection)
                                 :class "w-9"}]]
                         [:span {:class "block truncate"}
                          (:name connection)]]
                        (when (= (:name connection) (get filters "connection"))
                          [:> hero-micro-icon/CheckIcon {:class "w-4 h-4 text-black"}])]])]])]]]))]

        ;;  [:> ui/Popover {:class "relative"}
        ;;   (fn [params]
        ;;     (r/as-element
        ;;      [:<>
        ;;       [:> ui/Popover.Button {:class (str (if (get filters "type")
        ;;                                            "bg-gray-50 text-gray-600 border-gray-400 "
        ;;                                            "text-gray-500 border-gray-300 ")
        ;;                                          "w-full flex gap-small items-center cursor-pointer "
        ;;                                          "border rounded-md px-3 py-2 "
        ;;                                          "hover:bg-gray-50 hover:text-gray-600 hover:border-gray-400")}
        ;;        [:> hero-micro-icon/CircleStackIcon {:class "w-4 h-4"}]
        ;;        [:span {:class "text-sm font-semibold"}
        ;;         "Type"]
        ;;        (when (get filters "type")
        ;;          [:div {:class "flex items-center justify-center rounded-full h-4 w-4 bg-gray-800"}
        ;;           [:span {:class "text-white text-xxs font-bold"}
        ;;            "1"]])]
        ;;       [:> ui/Popover.Panel {:class (str "absolute mt-2 z-10 w-64 max-h-96 "
        ;;                                         "overflow-y-auto bg-white border border-gray-300 "
        ;;                                         "rounded-lg shadow-lg p-4")}
        ;;        [:div {:class (str "absolute w-2 h-2 "
        ;;                           "left-4 -top-1 border-gray-300 "
        ;;                           "bg-white border-t border-l "
        ;;                           "rounded transform rotate-45")}]
        ;;        [:div
        ;;         [:div {:class "mb-2"}
        ;;          [searchbox/main
        ;;           {:options connection-types-options
        ;;            :display-key :name
        ;;            :variant :small
        ;;            :searchable-keys [:text :value]
        ;;            :on-change-results-cb #(reset! searched-connections-types %)
        ;;            :hide-results-list true
        ;;            :placeholder "Search"
        ;;            :name "connection-search"
        ;;            :on-change #(reset! searched-criteria-connections-types %)
        ;;            :size :small}]]

        ;;         (if (and (empty? @searched-connections-types)
        ;;                  (> (count @searched-criteria-connections-types) 0))
        ;;           [:div {:class "px-regular py-large text-xs text-gray-700 italic"}
        ;;            "No connection type with this criteria"]

        ;;           [:div {:class "relative"}
        ;;            [:ul
        ;;             (for [type connection-types-search-options]
        ;;               ^{:key (:value type)}
        ;;               [:li {:class (str "flex justify-between cursor-pointer items-center gap-small "
        ;;                                 "text-sm text-gray-700 hover:bg-gray-200 rounded-md px-3 py-2")
        ;;                     :on-click (fn []
        ;;                                 (rf/dispatch [:audit->filter-sessions
        ;;                                               {"type" (if (= (:value type) (get filters "type"))
        ;;                                                         ""
        ;;                                                         (:value type))}])
        ;;                                 (.close params))}
        ;;                [:div {:class "w-full flex justify-between items-center gap-regular"}
        ;;                 [:span {:class "block truncate"}
        ;;                  (:text type)]
        ;;                 (when (= (:value type) (get filters "type"))
        ;;                   [:> hero-micro-icon/CheckIcon {:class "w-4 h-4 text-black"}])]])]])]]]))]
         ]))))

(defn empty-list-view []
  [:div {:class "pt-x-large"}
   [:figure
    {:class "w-1/6 mx-auto p-regular"}
    [:img {:src "/images/illustrations/pc.svg"
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
  (let [connections (rf/subscribe [:connections])
        user (rf/subscribe [:users->current-user])]
    (rf/dispatch [:connections->get-connections])
    (rf/dispatch [:users->get-current-user])
    (fn []
      [:div {:class "flex flex-col bg-white rounded-lg h-full p-6 overflow-y-auto"}
       [:header
        [connections-filters
         (:filters @connections)]]

       (if (and (= :loading (:status @connections)) (empty? (:results @connections)))
         [loading-list-view]

         [:div {:class "rounded-lg border bg-white h-full overflow-y-auto"}
          [:div {:class "relative h-full overflow-y-auto"}
                ;;  (when (and (= status :loading) (empty? (:data sessions)))
                ;;    [loading-list-view])
           (when (and (empty? (:results  @connections)) (not= (:status @connections) :loading))
             [empty-list-view])
           (doall
            (for [connection (:results @connections)]
              ^{:key (:id connection)}
              [:div {:class (str "border-b last:border-0 overflow-hidden hover:bg-gray-50 text-gray-700 "
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
                  [:div {:class "flex items-center gap-2 text-xs text-gray-700"}
                   [:div
                    [:> hero-micro-icon/TagIcon {:class "w-4 h-4"}]]
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

                (when (= "database" (:type connection))
                  [:div {:class "relative cursor-pointer"
                         :on-click (fn []
                                     (rf/dispatch [:connections->connection-connect (:name connection)])
                                     (rf/dispatch [:draggable-card->open-modal
                                                   [connection-connect/main]
                                                   :default
                                                   connection-connect/handle-close-modal]))}
                   [:> hero-micro-icon/SignalIcon {:class "w-6 h-6 text-gray-700"}]])

                (when (and (-> @user :data :admin?)
                           (not (= (:managed_by connection) "hoopagent")))
                  [:div {:class "relative cursor-pointer"
                         :on-click (fn []
                                     (rf/dispatch [:connections->get-connection {:connection-name (:name connection)}])
                                     (rf/dispatch [:open-modal [connection-form-modal/main :update]
                                                   :large]))}
                   [:> hero-micro-icon/AdjustmentsHorizontalIcon {:class "w-6 h-6 text-gray-700"}]])]]))]])])))
