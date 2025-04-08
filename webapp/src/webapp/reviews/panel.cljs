(ns webapp.reviews.panel
  (:require
   ["@headlessui/react" :as ui]
   ["@heroicons/react/16/solid" :as hero-micro-icon]
   ["lucide-react" :refer [ArrowRightLeft Check ListFilter]]
   ["react-tailwindcss-datepicker" :as Datepicker]
   [clojure.string :as string]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
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


        connections (rf/subscribe [:connections])
        searched-connections (r/atom nil)
        searched-criteria-connections (r/atom "")

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
    (rf/dispatch [:reviews-plugin->get-reviews {:status @review-status}])
    (rf/dispatch [:connections->get-connections])
    (rf/dispatch [:users->get-users])
    (fn []
      (let [connections-search-results (if (empty? @searched-connections)
                                         (:results @connections)
                                         @searched-connections)
            users-search-results (if (empty? @searched-users)
                                   (users-options @users)
                                   @searched-users)]
        [:div {:class "flex flex-col bg-white rounded-lg h-full p-6 overflow-y-auto"}
         [:div {:class "mb-regular flex items-center gap-2"}
;; User Filter
          [:> ui/Popover {:class "relative"}
           (fn [params]
             (r/as-element
              [:<>
               [:> ui/Popover.Button {:class (str (if (not (string/blank? @review-user))
                                                    "bg-gray-50 text-gray-600 border-gray-400 "
                                                    "text-gray-500 border-gray-300 ")
                                                  "w-full flex gap-small items-center cursor-pointer "
                                                  "border rounded-md px-3 py-2 "
                                                  "hover:bg-gray-50 hover:text-gray-600 hover:border-gray-400")}
                [:> hero-micro-icon/UserIcon {:class "w-4 h-4"}]
                [:span {:class "text-sm font-semibold"}
                 "User"]
                (when (not (string/blank? @review-user))
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
                                                        :end_date (iso-date "end_date" (.-endDate @date))}])
                                         (.close params))}
                        [:div {:class "w-full flex justify-between items-center gap-regular"}
                         [:span {:class "block truncate"}
                          (:text user)]
                         (when (= (:value user) @review-user)
                           [:> hero-micro-icon/CheckIcon {:class "w-4 h-4 text-black"}])]])]])]]]))]

        ;; Status Filter
          [:> ui/Popover {:class "relative"}
           (fn [params]
             (r/as-element
              [:<>
               [:> ui/Popover.Button {:class (str (if (not (string/blank? @review-status))
                                                    "bg-gray-50 text-gray-600 border-gray-400 "
                                                    "text-gray-500 border-gray-300 ")
                                                  "w-full flex gap-small items-center cursor-pointer "
                                                  "border rounded-md px-3 py-2 "
                                                  "hover:bg-gray-50 hover:text-gray-600 hover:border-gray-400")}
                [:> ListFilter {:size 16}]
                [:span {:class "text-sm font-semibold"}
                 "Status"]
                (when (not (string/blank? @review-status))
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
                                                       :end_date (iso-date "end_date" (.-endDate @date))}])
                                        (.close params))}
                       [:div {:class "w-full flex justify-between items-center gap-regular"}
                        [:div {:class "flex items-center gap-small"}
                         [:span {:class "block truncate"}
                          (:text status)]]
                        (when (= (:value status) @review-status)
                          [:> Check {:size 16}])]]))]]]]]))]

        ;; Connection Filter
          [:> ui/Popover {:class "relative"}
           (fn [params]
             (r/as-element
              [:<>
               [:> ui/Popover.Button {:class (str (if (not (string/blank? @review-connection))
                                                    "bg-gray-50 text-gray-600 border-gray-400 "
                                                    "text-gray-500 border-gray-300 ")
                                                  "w-full flex gap-small items-center cursor-pointer "
                                                  "border rounded-md px-3 py-2 "
                                                  "hover:bg-gray-50 hover:text-gray-600 hover:border-gray-400")}
                [:> ArrowRightLeft {:size 16}]
                [:span {:class "text-sm font-semibold"}
                 "Connection"]
                (when (not (string/blank? @review-connection))
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
                                          (reset! review-connection (:name connection))
                                          (rf/dispatch [:reviews-plugin->get-reviews
                                                        {:status @review-status
                                                         :user @review-user
                                                         :connection (:name connection)
                                                         :start_date (iso-date "start_date" (.-startDate @date))
                                                         :end_date (iso-date "end_date" (.-endDate @date))}])
                                          (.close params))}
                         [:div {:class "w-full flex justify-between items-center gap-regular"}
                          [:div {:class "flex items-center gap-small"}
                           [:figure {:class "w-5"}
                            [:img {:src  (connection-constants/get-connection-icon connection)
                                   :class "w-9"}]]
                           [:span {:class "block truncate"}
                            (:name connection)]]
                          (when (= (:name connection) @review-connection)
                            [:> Check {:size 16}])]]))]])]]]))]

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
                                                  "bg-gray-50 text-gray-600 border-gray-400 "
                                                  "text-gray-500 border-gray-300 ")
                                                "pl-10 py-2 rounded-md text-sm font-semibold "
                                                "w-full border "
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
