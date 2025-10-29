(ns webapp.audit.views.audit-filters
  (:require ["@heroicons/react/16/solid" :as hero-micro-icon]
            ["@radix-ui/themes" :refer [Popover Button]]
            ["react-tailwindcss-datepicker" :as Datepicker]
            [clojure.string :as string]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.searchbox :as searchbox]
            [webapp.connections.constants :as connection-constants]))

(defn- form [filters]
  (let [user (rf/subscribe [:users->current-user])
        users (rf/subscribe [:users])
        searched-users (r/atom nil)
        searched-criteria-users (r/atom "")

        connections (rf/subscribe [:connections])
        searched-connections (r/atom nil)
        searched-criteria-connections (r/atom "")

        connection-types-options [{:text "Custom" :value "custom"}
                                  {:text "Database" :value "database"}
                                  {:text "Application" :value "application"}]
        searched-connections-types (r/atom nil)
        searched-criteria-connections-types (r/atom "")

        date (r/atom #js{"startDate" (if-let [date (get filters "start_date")]
                                       (subs date 0 10) "")
                         "endDate" (if-let [date (get filters "end_date")]
                                     (subs date 0 10) "")})
        users-options (fn [users]
                        (map #(into {} {:value (:id %)
                                        :text (:email %)}) users))
        dispatch-date (fn [date]
                        (let [iso-date (fn [filter-key date]
                                         (when (not (string/blank? date))
                                           (.toISOString
                                            (new js/Date
                                                 (if (= filter-key "start_date")
                                                   (str date " 00:00:00.000Z")
                                                   (str date " 23:59:59.000Z"))))))]
                          (rf/dispatch [:audit->filter-sessions {"start_date" (iso-date "start_date" (.-startDate date))
                                                                 "end_date" (iso-date "end_date" (.-endDate date))}])))]
    (fn [filters]
      (let [connections-search-results (if (empty? @searched-connections)
                                         (:results @connections)
                                         @searched-connections)
            users-search-results (if (empty? @searched-users)
                                   (users-options (sort-by :name @users))
                                   @searched-users)
            connection-types-search-options (if (empty? @searched-connections-types)
                                              connection-types-options
                                              @searched-connections-types)]
        [:div {:class "flex gap-regular flex-wrap mb-4"}
         [:> Popover.Root
          [:> Popover.Trigger {:asChild true}
           [:> Button {:size "3"
                       :variant (if (get filters "user") "soft" "surface")
                       :color "gray"
                       :on-click (fn []
                                   (reset! searched-users nil)
                                   (reset! searched-criteria-users ""))}
            [:> hero-micro-icon/UserIcon {:class "w-4 h-4"}]
            [:span {:class "text-sm font-semibold"}
             "User"]
            (when (get filters "user")
              [:div {:class "flex items-center justify-center rounded-full h-4 w-4 bg-gray-800"}
               [:span {:class "text-white text-xxs font-bold"}
                "1"]])]]
          [:> Popover.Content {:size "2" :style {:width "384px" :max-height "384px"}}
           [:div {:class "w-full max-h-96 overflow-y-auto"}
            [:div
             [:div {:class "mb-2"}
              [searchbox/main
               {:options (users-options (sort-by :name @users))
                :display-key :text
                :variant :small
                :searchable-keys [:value :text]
                :on-change-results-cb #(reset! searched-users %)
                :hide-results-list true
                :placeholder "Search"
                :name "users-search"
                :on-change #(reset! searched-criteria-users %)
                :loading? (empty? (users-options @users))
                :size :small}]]

             (if (and (empty? @searched-users)
                      (> (count @searched-criteria-users) 0))
               [:div {:class "px-regular py-large text-xs text-gray-700 italic"}
                "No user with this criteria"]

               [:div {:class "relative"}
                [:ul
                 (doall
                  (for [user users-search-results]
                    ^{:key (:text user)}
                    [:li {:class (str "flex justify-between cursor-pointer items-center gap-small "
                                      "text-sm text-gray-700 hover:bg-gray-200 rounded-md px-3 py-2")
                          :on-click (fn []
                                      (rf/dispatch [:audit->filter-sessions
                                                    {"user" (if (= (:value user) (get filters "user"))
                                                              ""
                                                              (:value user))}]))}
                     [:div {:class "w-full flex justify-between items-center gap-regular"}
                      [:span {:class "block truncate"}
                       (:text user)]
                      (when (= (:value user) (get filters "user"))
                        [:> hero-micro-icon/CheckIcon {:class "w-4 h-4 text-black"}])]]))]])]]]

          [:> Popover.Root
           [:> Popover.Trigger {:asChild true}
            [:> Button {:size "3"
                        :variant (if (get filters "connection") "soft" "surface")
                        :color "gray"
                        :on-click (fn []
                                    (reset! searched-connections nil)
                                    (reset! searched-criteria-connections ""))}
             [:> hero-micro-icon/ArrowsRightLeftIcon {:class "w-4 h-4"}]
             [:span {:class "text-sm font-semibold"}
              "Connection"]
             (when (get filters "connection")
               [:div {:class "flex items-center justify-center rounded-full h-4 w-4 bg-gray-800"}
                [:span {:class "text-white text-xxs font-bold"}
                 "1"]])]]
           [:> Popover.Content {:size "2" :style {:width "384px" :max-height "384px"}}
            [:div {:class "w-full max-h-96 overflow-y-auto"}
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
                 :name "connection-search"
                 :on-change #(reset! searched-criteria-connections %)
                 :loading? (empty? (:results @connections))
                 :size :small}]]

              (if (and (empty? @searched-connections)
                       (> (count @searched-criteria-connections) 0))
                [:div {:class "px-regular py-large text-xs text-gray-700 italic"}
                 "No connections with this criteria"]

                [:div {:class "relative"}
                 [:ul
                  (doall
                   (for [connection connections-search-results]
                     ^{:key (:name connection)}
                     [:li {:class (str "flex justify-between cursor-pointer items-center gap-small "
                                       "text-sm text-gray-700 hover:bg-gray-200 rounded-md px-3 py-2")
                           :on-click (fn []
                                       (rf/dispatch [:audit->filter-sessions
                                                     {"connection" (if (= (:name connection) (get filters "connection"))
                                                                     ""
                                                                     (:name connection))}]))}
                      [:div {:class "w-full flex justify-between items-center gap-regular"}
                       [:div {:class "flex items-center gap-small"}
                        [:figure {:class "w-5"}
                         [:img {:src  (connection-constants/get-connection-icon connection)
                                :class "w-9"}]]
                        [:span {:class "block truncate"}
                         (:name connection)]]
                       (when (= (:name connection) (get filters "connection"))
                         [:> hero-micro-icon/CheckIcon {:class "w-4 h-4 text-black"}])]]))]])]]]]

          [:> Popover.Root
           [:> Popover.Trigger {:asChild true}
            [:> Button {:size "3"
                        :variant (if (get filters "type") "soft" "surface")
                        :color "gray"
                        :on-click (fn []
                                    (reset! searched-connections-types nil)
                                    (reset! searched-criteria-connections-types ""))}
             [:> hero-micro-icon/CircleStackIcon {:class "w-4 h-4"}]
             [:span {:class "text-sm font-semibold"}
              "Type"]
             (when (get filters "type")
               [:div {:class "flex items-center justify-center rounded-full h-4 w-4 bg-gray-800"}
                [:span {:class "text-white text-xxs font-bold"}
                 "1"]])]]
           [:> Popover.Content {:size "2" :style {:width "256px" :max-height "384px"}}
            [:div {:class "w-full max-h-96 overflow-y-auto"}
             [:div
              [:div {:class "mb-2"}
               [searchbox/main
                {:options connection-types-options
                 :display-key :name
                 :variant :small
                 :searchable-keys [:text :value]
                 :on-change-results-cb #(reset! searched-connections-types %)
                 :hide-results-list true
                 :placeholder "Search"
                 :name "connection-search"
                 :on-change #(reset! searched-criteria-connections-types %)
                 :size :small}]]

              (if (and (empty? @searched-connections-types)
                       (> (count @searched-criteria-connections-types) 0))
                [:div {:class "px-regular py-large text-xs text-gray-700 italic"}
                 "No connection type with this criteria"]

                [:div {:class "relative"}
                 [:ul
                  (doall
                   (for [type connection-types-search-options]
                     ^{:key (:value type)}
                     [:li {:class (str "flex justify-between cursor-pointer items-center gap-small "
                                       "text-sm text-gray-700 hover:bg-gray-200 rounded-md px-3 py-2")
                           :on-click (fn []
                                       (rf/dispatch [:audit->filter-sessions
                                                     {"type" (if (= (:value type) (get filters "type"))
                                                               ""
                                                               (:value type))}]))}
                      [:div {:class "w-full flex justify-between items-center gap-regular"}
                       [:span {:class "block truncate"}
                        (:text type)]
                       (when (= (:value type) (get filters "type"))
                         [:> hero-micro-icon/CheckIcon {:class "w-4 h-4 text-black"}])]]))]])]]]

           [:> Datepicker {:value @date
                           :placeholder "Period"
                           :separator "-"
                           :displayFormat "DD/MM/YYYY"
                           :containerClassName "relative w-64 text-gray-700"
                           :toggleClassName (str "absolute rounded-l-lg "
                                                 "text-gray-500 "
                                                 "left-0 h-full px-3 "
                                                 "focus:outline-none disabled:opacity-40 "
                                                 "disabled:cursor-not-allowed")
                           :inputClassName (str (if (or (.-startDate @date) (.-endDate @date))
                                                  " border-gray-300 "
                                                  " border-gray-400 ")
                                                "pl-10 py-2 w-full rounded-lg text-gray-600 "
                                                "font-semibold text-sm focus:ring-0 "
                                                "border h-[40px] "
                                                "placeholder:text-gray-500 "
                                                "hover:bg-gray-50 hover:text-gray-600 hover:border-gray-400 "
                                                "focus:bg-gray-50 focus:text-gray-600 focus:border-gray-400")
                           :useRange false
                           :showShortcuts true
                           :onChange (fn [v]
                                       (reset! date v)
                                       (dispatch-date v))}]]]]))))

(defn audit-filters [_]
  (rf/dispatch [:connections->get-connections])
  (rf/dispatch [:users->get-users])
  (fn [filters]
    [form filters]))
