(ns webapp.reviews.panel
  (:require [clojure.string :as string]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.combobox :as combobox]
            [webapp.components.loaders :as loaders]
            [webapp.reviews.review-detail :as review-detail]
            [webapp.reviews.review-item :as review-item]))

(defn- list-item [_]
  (let [active-review (rf/subscribe [:reviews-plugin->review-details])]
    (fn [review]
      (let [selected (when (= (-> @active-review :review :id)
                              (:id review))
                       :selected)]
        [review-item/review-item selected review]))))

(defn- empty-list-view []
  [:div {:class "pt-x-large"}
   [:figure
    {:class "w-3/4 mx-auto p-regular"}
    [:img {:src "/images/illustrations/gameboy.svg"
           :class "w-full"}]]
   [:div {:class "px-large text-center"}
    [:div {:class "text-gray-700 text-sm font-bold"}
     "All caught up!"]
    [:div {:class "text-gray-500 text-xs"}
     "Take a break, play a game or fill your cup of coffee."]]])

(defn- reviews-list [_ _]
  (let [review-status-options [{:text "Pending" :value "PENDING"}
                               {:text "Approved" :value "APPROVED"}]]
    (fn [reviews review-status]
      [:<>
       [:div {:class "mx-small mb-regular"}
        [combobox/main {:options review-status-options
                        :selected @review-status
                        :label "Status"
                        :clear? true
                        :default-value "Select a status"
                        :placeholder "Select one"
                        :list-classes "min-w-64"
                        :on-change #(reset! review-status %)
                        :name "select-type"}]]
       (when (empty? reviews)
         [empty-list-view])
       (doall
        (for [review reviews]
          ^{:key (str (:id review) (:group review))}
          [list-item review]))])))

(defn- loading-list-view []
  [:div {:class "flex items-center justify-center h-full"}
   [loaders/simple-loader]])

(defn panel []
  (let [review-status (r/atom "PENDING")
        reviews (rf/subscribe [:reviews-plugin->reviews])
        filtering (fn [reviews status]
                    (if (string/blank? status)
                      reviews
                      (filter #(= status (:status %)) reviews)))]
    (rf/dispatch [:reviews-plugin->get-reviews])
    (fn []
      [:div {:class "lg:h-full grid grid-cols-1 lg:grid-cols-9 gap-regular overflow-hidden"}
       [:div {:class "bg-white px-regular py-regular rounded-lg lg:col-span-3 overflow-auto max-h-screen"}
        (if (= :loading (-> @reviews :status))
          [loading-list-view]
          [reviews-list (filtering @reviews @review-status) review-status])]
       [:div {:class "bg-white px-regular py-regular rounded-lg lg:col-span-6 p-regular overflow-auto"}
        [review-detail/review-detail]]])))
