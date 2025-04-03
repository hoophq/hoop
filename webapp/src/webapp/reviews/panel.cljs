(ns webapp.reviews.panel
  (:require [clojure.string :as string]
            [re-frame.core :as rf]
            [reagent.core :as r]
            ["react-tailwindcss-datepicker" :as Datepicker]
            [webapp.components.loaders :as loaders]
            [webapp.components.forms :as forms]
            [webapp.reviews.review-item :as review-item]
            [webapp.connections.constants :as connection-constants]
            [webapp.config :as config]))

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
  (let [review-status (r/atom "PENDING")
        review-connection (r/atom "")
        date (r/atom #js{"startDate" "" "endDate" ""})
        reviews (rf/subscribe [:reviews-plugin->reviews])
        connections (rf/subscribe [:connections])
        review-status-options [{:text "Pending" :value "PENDING"}
                               {:text "Approved" :value "APPROVED"}
                               {:text "Rejected" :value "REJECTED"}]
        dispatch-date (fn [date-obj]
                        (let [iso-date (fn [filter-key date]
                                         (when (not (string/blank? date))
                                           (.toISOString
                                            (new js/Date
                                                 (if (= filter-key "start_date")
                                                   (str date " 00:00:00.000Z")
                                                   (str date " 23:59:59.000Z"))))))]
                          (rf/dispatch [:reviews-plugin->get-reviews
                                        {:status @review-status
                                         :connection @review-connection
                                         :start_date (iso-date "start_date" (.-startDate date-obj))
                                         :end_date (iso-date "end_date" (.-endDate date-obj))}])))]
    (rf/dispatch [:reviews-plugin->get-reviews {:status @review-status}])
    (rf/dispatch [:connections->get-connections])
    (fn []
      [:div {:class "flex flex-col bg-white rounded-lg h-full p-6 overflow-y-auto"}
       [:div {:class "mb-regular flex flex-wrap gap-regular"}
        [forms/select
         {:options review-status-options
          :label "Status"
          :placeholder "Select status"
          :selected @review-status
          :size "2"
          :on-change #(do
                        (reset! review-status %)
                        (rf/dispatch [:reviews-plugin->get-reviews
                                      {:status %
                                       :connection @review-connection
                                       :start_date (.-startDate @date)
                                       :end_date (.-endDate @date)}]))}]

        [forms/select
         {:options (map (fn [conn] {:text (:name conn) :value (:name conn)})
                        (get @connections :results []))
          :label "Connection"
          :placeholder "All connections"
          :selected @review-connection
          :size "2"
          :on-change #(do
                        (reset! review-connection %)
                        (rf/dispatch [:reviews-plugin->get-reviews
                                      {:status @review-status
                                       :connection %
                                       :start_date (.-startDate @date)
                                       :end_date (.-endDate @date)}]))}]

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
                                             "pl-10 py-2 w-full rounded-md text-gray-600 "
                                             "font-semibold text-sm focus:ring-0 "
                                             "border "
                                             "placeholder:text-gray-500 "
                                             "hover:bg-gray-50 hover:text-gray-600 hover:border-gray-400 "
                                             "focus:bg-gray-50 focus:text-gray-600 focus:border-gray-400")
                        :useRange false
                        :showShortcuts true
                        :onChange (fn [v]
                                    (reset! date v)
                                    (dispatch-date v))}]]

       (if (= :loading (-> @reviews :status))
         [loading-list-view]

         [:div {:class "rounded-lg border bg-white h-full overflow-y-auto"}
          [reviews-list (:results @reviews) @reviews]])])))
