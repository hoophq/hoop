(ns webapp.connections.views.connection-list
  (:require ["@heroicons/react/16/solid" :as hero-micro-icon]
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

(defn- tooltip [text position]
  [:div {:class (str "absolute -bottom-10 flex-col hidden mt-6 w-max "
                     "group-hover:flex items-center -translate-x-1/2 z-50 "
                     (if (= position "left")
                       "-left-4"
                       "left-1/2"))}
   [:div {:class (str "relative w-3 h-3 -mb-2 bg-gray-900 transform rotate-45 z-50 "
                      (if (= position "left")
                        "left-[30px]"
                        ""))}]
   [:span {:class (str "relative bg-gray-900 rounded-md z-50 "
                       "py-1.5 px-3.5 text-xs text-white leading-none whitespace-no-wrap shadow-lg")}
    text]])

(defn panel [_]
  (let [connections (rf/subscribe [:connections])
        user (rf/subscribe [:users->current-user])
        search-focused (r/atom false)
        searched-connections (r/atom nil)
        searched-criteria-connections (r/atom "")
        connections-search-status (r/atom nil)]
    (rf/dispatch [:connections->get-connections])
    (rf/dispatch [:users->get-user])
    (fn []
      (let [connections-search-results (if (empty? @searched-connections)
                                         (:results @connections)
                                         @searched-connections)]
        [:div {:class "flex flex-col bg-white rounded-lg h-full p-6 overflow-y-auto"}
         (when (-> @user :data :admin?)
           [:div {:class "absolute top-10 right-4 sm:right-6 lg:top-16 lg:right-20"}
            [button/tailwind-primary {:text "Add connection"
                                      :on-click (fn []
                                                  (rf/dispatch [:navigate :create-connection]))}]])
         [:header
          [:div {:class "mb-6"}
           [searchbox/main
            {:options (:results @connections)
             :display-key :name
             :searchable-keys [:name :type :tags :status]
             :on-change-results-cb #(reset! searched-connections %)
             :hide-results-list true
             :placeholder "Search by connection name, type, status or anything"
             :on-focus #(reset! search-focused true)
             :on-blur #(reset! search-focused false)
             :name "connection-search"
             :on-change #(reset! searched-criteria-connections %)
             :loading? (= @connections-search-status :loading)
             :size :small
             :icon-position "left"}]]]

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
                      [:div {:class "relative cursor-pointer group"
                             :on-click (fn []
                                         (if (= (.getItem js/localStorage "hoop-connect-setup") "skipped")
                                           (rf/dispatch [:connections->start-connect (:name connection)])
                                           (rf/dispatch [:connections->open-connect-setup (:name connection)])))}
                       [tooltip "Hoop Access" (when (not (-> @user :data :admin?))
                                                "left")]
                       [:> hero-micro-icon/SignalIcon {:class "w-6 h-6 text-gray-700"}]])

                    (when (and (-> @user :data :admin?)
                               (not (= (:managed_by connection) "hoopagent")))
                      [:div {:class "relative cursor-pointer group"
                             :on-click (fn []
                                         (rf/dispatch [:plugins->get-my-plugins])
                                         (rf/dispatch [:connections->get-connection {:connection-name (:name connection)}])
                                         (rf/dispatch [:modal->open {:content [:f> create-update-connection/main :update connection]}]))}
                       [tooltip "Configure" "left"]
                       [:> hero-micro-icon/AdjustmentsHorizontalIcon {:class "w-6 h-6 text-gray-700"}]])]])))]])]))))
