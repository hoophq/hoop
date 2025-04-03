(ns webapp.reviews.panel
  (:require [clojure.string :as string]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.loaders :as loaders]
            [webapp.components.forms :as forms]
            [webapp.reviews.review-item :as review-item]
            [webapp.config :as config]))

(defn- list-item [review]
  [review-item/review-item nil review])

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

(defn- reviews-list [reviews]
  [:div {:class "relative h-full overflow-y-auto"}
   (when (empty? reviews)
     [empty-list-view])
   (doall
    (for [review reviews]
      ^{:key (str (:id review) (:group review))}
      [:div {:class (when (= :loading (:status reviews)) "opacity-50 pointer-events-none")}
       [list-item review]]))
   (when (and (not-empty reviews) (> (count reviews) 10))
     [:div {:class "py-regular text-center"}
      [:a
       {:href "#"
        :class "text-sm text-blue-500"
        :on-click #(rf/dispatch [:reviews-plugin->get-more-reviews])}
       "Load more reviews"]])])

(defn panel []
  (let [review-status (r/atom "PENDING")
        reviews (rf/subscribe [:reviews-plugin->reviews])
        review-status-options [{:text "Pending" :value "PENDING"}
                               {:text "Approved" :value "APPROVED"}
                               {:text "Rejected" :value "REJECTED"}]
        filtering (fn [reviews status]
                    (if (string/blank? status)
                      reviews
                      (filter #(= status (:status %)) reviews)))]
    (rf/dispatch [:reviews-plugin->get-reviews])
    (fn []
      [:div {:class "flex flex-col bg-white rounded-lg h-full p-6 overflow-y-auto"}
       [:div {:class "mb-regular"}
        [forms/select
         {:options review-status-options
          :label "Status"
          :placeholder "Select status"
          :selected @review-status
          :size "2"
          :on-change #(reset! review-status %)}]]

       (if (= :loading (-> @reviews :status))
         [loading-list-view]

         [:div {:class "rounded-lg border bg-white h-full overflow-y-auto"}
          [reviews-list (filtering (:results @reviews) @review-status)]])])))
