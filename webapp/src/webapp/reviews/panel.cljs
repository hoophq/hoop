(ns webapp.reviews.panel
  (:require
   ["@heroicons/react/16/solid" :as hero-micro-icon]
   ["@radix-ui/themes" :refer [Popover Button]]
   ["lucide-react" :refer [ArrowRightLeft Check ListFilter Search]]
   ["react-tailwindcss-datepicker" :as Datepicker]
   [clojure.string :as string]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.components.infinite-scroll :refer [infinite-scroll]]
   [webapp.components.searchbox :as searchbox]
   [webapp.config :as config]
   [webapp.connections.constants :as connection-constants]
   [webapp.reviews.review-item :as review-item]))

(defn- list-item [session]
  [review-item/review-item nil session])

(defn- empty-list-view []
  [:div {:class "pt-x-large"}
   [:figure
    {:class "w-1/6 mx-auto p-regular"}
    [:img {:src (str config/webapp-url "/images/illustrations/gameboy.svg")
           :class "w-full"}]]
   [:div {:class "px-large text-center"}
    [:div {:class "text-gray-700 text-sm font-bold"}
     "All caught up!"]
    [:div {:class "text-gray-500 text-xs"}
     "Take a break, play a game or fill your cup of coffee."]]])

(defn- loading-list-view []
  [:div {:class "flex items-center justify-center h-full"}
   [loaders/simple-loader]])

(defn- reviews-list [sessions reviews-data]
  [:div {:class "relative h-full overflow-y-auto"}
   (when (empty? sessions)
     [empty-list-view])
   (doall
    (for [session sessions]
      ^{:key (str (:id session) (-> session :review :id))}
      [:div {:class (when (= :loading (:status reviews-data)) "opacity-50 pointer-events-none")}
       [list-item session]]))
   (when (:has_next_page reviews-data)
     [:div {:class "py-regular text-center"}
      [:a
       {:href "#"
        :class "text-sm text-blue-500"
        :on-click #(rf/dispatch [:reviews-plugin->load-more-reviews])}
       "Load more reviews"]])])

(defn panel []
  (let [current-user (rf/subscribe [:users->current-user])
        review-status (r/atom "PENDING")
        review-connection (r/atom "")
        review-user (r/atom (get-in @current-user [:data :email] ""))
        date (r/atom #js{"startDate" "" "endDate" ""})
        reviews (rf/subscribe [:reviews-plugin->reviews])

        users (rf/subscribe [:users])
        searched-users (r/atom nil)
        searched-criteria-users (r/atom "")
        users-options (fn [users]
                        (map #(into {} {:value (:email %)
                                        :text (:email %)}) users))


        connections (rf/subscribe [:connections->pagination])
        search-term-connections (r/atom "")
        search-debounce-timer-connections (r/atom nil)

        review-status-options [{:text "Pending" :value "PENDING"}
                               {:text "Approved" :value "APPROVED"}
                               {:text "Rejected" :value "REJECTED"}]

        iso-date (fn [filter-key date]
                   (when (not (string/blank? date))
                     (.toISOString
                      (new js/Date
                           (if (= filter-key "start_date")
                             (str date " 00:00:00.000Z")
                             (str date " 23:59:59.000Z"))))))

        dispatch-date (fn [date-obj]
                        (rf/dispatch [:reviews-plugin->get-reviews
                                      {:status @review-status
                                       :user @review-user
                                       :connection @review-connection
                                       :start_date (iso-date "start_date" (.-startDate date-obj))
                                       :end_date (iso-date "end_date" (.-endDate date-obj))}]))]
    (rf/dispatch [:reviews-plugin->get-reviews {:status @review-status
                                                :user @review-user}])
    (rf/dispatch [:connections/get-connections-paginated {:page 1 :force-refresh? true}])
    (rf/dispatch [:users->get-users])
    (fn []
      (let [connections-data (or (:data @connections) [])
            connections-loading? (:loading @connections)
            has-more? (:has-more? @connections)
            current-page (:current-page @connections 1)
            users-search-results (if (empty? @searched-users)
                                   (users-options @users)
                                   @searched-users)]
        [:div {:class "flex flex-col bg-white rounded-lg h-full p-6 overflow-y-auto"}
         [:div {:class "mb-regular flex items-center gap-2"}
          ;; User Filter
          [:> Popover.Root
           [:> Popover.Trigger {:asChild true}
            [:> Button {:size "3"
                        :variant (if (not (string/blank? @review-user)) "soft" "surface")
                        :color "gray"
                        :on-click (fn []
                                    (reset! searched-users nil)
                                    (reset! searched-criteria-users ""))}
             [:> hero-micro-icon/UserIcon {:class "w-4 h-4"}]
             [:span {:class "text-sm font-semibold"}
              "User"]
             (when (not (string/blank? @review-user))
               [:div {:class "flex items-center justify-center rounded-full h-4 w-4 bg-gray-800"}
                [:span {:class "text-white text-xxs font-bold"}
                 "1"]])]]
           [:> Popover.Content {:size "2" :style {:width "384px" :max-height "384px"}}
            [:div {:class "w-full max-h-96 overflow-y-auto"}
             [:div
              [:div {:class "mb-2"}
               [searchbox/main
                {:options (users-options @users)
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
                                       (reset! review-user (:value user))
                                       (rf/dispatch [:reviews-plugin->get-reviews
                                                     {:status @review-status
                                                      :user (:value user)
                                                      :connection @review-connection
                                                      :start_date (iso-date "start_date" (.-startDate @date))
                                                      :end_date (iso-date "end_date" (.-endDate @date))}]))}
                      [:div {:class "w-full flex justify-between items-center gap-regular"}
                       [:span {:class "block truncate"}
                        (:text user)]
                       (when (= (:value user) @review-user)
                         [:> hero-micro-icon/CheckIcon {:class "w-4 h-4 text-black"}])]]))]])]]]]

          ;; Status Filter
          [:> Popover.Root
           [:> Popover.Trigger {:asChild true}
            [:> Button {:size "3"
                        :variant (if (not (string/blank? @review-status)) "soft" "surface")
                        :color "gray"
                        :on-click (fn []
                                    (reset! search-term-connections nil))}
             [:> ListFilter {:size 16}]
             [:span {:class "text-sm font-semibold"}
              "Status"]
             (when (not (string/blank? @review-status))
               [:div {:class "flex items-center justify-center rounded-full h-4 w-4 bg-gray-800"}
                [:span {:class "text-white text-xxs font-bold"}
                 "1"]])]]
           [:> Popover.Content {:size "2" :style {:width "384px" :max-height "384px"}}
            [:div {:class "w-full max-h-96 overflow-y-auto"}
             [:div
              [:div {:class "relative"}
               [:ul
                (doall
                 (for [status review-status-options]
                   ^{:key (:text status)}
                   [:li {:class (str "flex justify-between cursor-pointer items-center gap-small "
                                     "text-sm text-gray-700 hover:bg-gray-200 rounded-md px-3 py-2")
                         :on-click (fn []
                                     (reset! review-status (:value status))
                                     (rf/dispatch [:reviews-plugin->get-reviews
                                                   {:status (:value status)
                                                    :user @review-user
                                                    :connection @review-connection
                                                    :start_date (iso-date "start_date" (.-startDate @date))
                                                    :end_date (iso-date "end_date" (.-endDate @date))}]))}
                    [:div {:class "w-full flex justify-between items-center gap-regular"}
                     [:div {:class "flex items-center gap-small"}
                      [:span {:class "block truncate"}
                       (:text status)]]
                     (when (= (:value status) @review-status)
                       [:> Check {:size 16}])]]))]]]]]]

          ;; Connection Filter
          [:> Popover.Root
           [:> Popover.Trigger {:asChild true}
            [:> Button {:size "3"
                        :variant (if (not (string/blank? @review-connection)) "soft" "surface")
                        :color "gray"}
             [:> ArrowRightLeft {:size 16}]
             [:span {:class "text-sm font-semibold"}
              (if (string/blank? @review-connection)
                "Resource Role"
                @review-connection)]
             (when (not (string/blank? @review-connection))
               [:div {:class "flex items-center justify-center rounded-full h-4 w-4 bg-gray-800"}
                [:span {:class "text-white text-xxs font-bold"}
                 "1"]])]]
           [:> Popover.Content {:size "2" :style {:width "384px"}}
            [:div {:class "w-full max-h-96"}
             [:div
              ;; Clear filter option
              (when (not (string/blank? @review-connection))
                [:div {:class "mb-2 pb-2 border-b border-gray-200"}
                 [:div {:class (str "flex cursor-pointer items-center gap-2 "
                                    "text-sm text-gray-700 hover:bg-gray-200 rounded-md px-3 py-2")
                        :on-click (fn []
                                    (reset! review-connection "")
                                    (rf/dispatch [:reviews-plugin->get-reviews
                                                  {:status @review-status
                                                   :user @review-user
                                                   :connection ""
                                                   :start_date (iso-date "start_date" (.-startDate @date))
                                                   :end_date (iso-date "end_date" (.-endDate @date))}]))}
                  [:span "Clear filter"]]])

              [:div {:class "mb-2 relative"}
               [:input {:type "text"
                        :class "w-full pr-10 pl-3 py-2 border border-gray-300 rounded-md text-sm"
                        :placeholder "Search resource roles"
                        :value @search-term-connections
                        :onChange (fn [e]
                                    (let [value (-> e .-target .-value)
                                          trimmed (string/trim value)
                                          should-search? (or (string/blank? trimmed)
                                                             (> (count trimmed) 2))
                                          request (cond-> {:page 1 :force-refresh? true}
                                                    (seq trimmed) (assoc :search trimmed))]
                                      (reset! search-term-connections value)
                                      (when @search-debounce-timer-connections
                                        (js/clearTimeout @search-debounce-timer-connections))
                                      (if should-search?
                                        (reset! search-debounce-timer-connections
                                                (js/setTimeout
                                                 (fn []
                                                   (rf/dispatch [:connections/get-connections-paginated request]))
                                                 300))
                                        (reset! search-debounce-timer-connections nil))))}]
               [:> Search {:class "absolute right-3 top-1/2 transform -translate-y-1/2 text-gray-400" :size 16}]]

              (if (> (count connections-data) 0)
                [:div {:class "relative"}
                 [:ul
                  [infinite-scroll
                   {:on-load-more (fn []
                                    (when (not connections-loading?)
                                      (let [next-page (inc current-page)
                                            active-search (:active-search @connections)
                                            next-request (cond-> {:page next-page
                                                                  :force-refresh? false}
                                                           (not (string/blank? active-search)) (assoc :search active-search))]
                                        (rf/dispatch [:connections/get-connections-paginated next-request]))))
                    :has-more? has-more?
                    :loading? connections-loading?}
                   (doall
                    (for [connection connections-data]
                      ^{:key (:name connection)}
                      [:li {:class (str "flex justify-between cursor-pointer items-center gap-2 "
                                        "text-sm text-gray-700 hover:bg-gray-200 rounded-md px-3 py-2")
                            :on-click (fn []
                                        (reset! review-connection (:name connection))
                                        (rf/dispatch [:reviews-plugin->get-reviews
                                                      {:status @review-status
                                                       :user @review-user
                                                       :connection (:name connection)
                                                       :start_date (iso-date "start_date" (.-startDate @date))
                                                       :end_date (iso-date "end_date" (.-endDate @date))}]))}
                       [:div {:class "w-full flex justify-between items-center gap-3"}
                        [:div {:class "flex items-center gap-2"}
                         [:figure {:class "w-4"}
                          [:img {:src (connection-constants/get-connection-icon connection)
                                 :class "w-full"}]]
                         [:span {:class "block truncate"}
                          (:name connection)]]
                        (when (= (:name connection) @review-connection)
                          [:> Check {:size 16}])]]))]]]
                [:div {:class "px-3 py-4 text-xs text-gray-700 italic"}
                 (if (seq @search-term-connections)
                   "No connections found matching your search"
                   "No connections with this criteria")])]]]]

          ;; Date Filter
          [:div
           [:> Datepicker {:value @date
                           :placeholder "Period"
                           :separator "-"
                           :displayFormat "DD/MM/YYYY"
                           :containerClassName "relative w-full min-w-[240px] text-gray-700"
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
                                       (dispatch-date v))}]]]

         (if (= :loading (-> @reviews :status))
           [loading-list-view]

           [:div {:class "rounded-lg border bg-white h-full overflow-y-auto"}
            [reviews-list (:results @reviews) @reviews]])]))))
