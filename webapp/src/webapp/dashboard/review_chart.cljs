(ns webapp.dashboard.review-chart
  (:require ["@radix-ui/themes" :refer [Box Flex Heading Section
                                        SegmentedControl Text]]
            ["recharts" :as recharts]
            [cljs-time.coerce :as coerce]
            [cljs-time.core :as time]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.charts :as charts]))

(defn parse-date [date-str]
  (coerce/from-string date-str))

(defn sort-reviews-by-date [reviews]
  (sort-by #(parse-date (:date %)) time/before? reviews))

(defn aggregate-by-date [reviews]
  (reduce
   (fn [acc review]
     (let [date (.toISOString
                 (new js/Date (:created_at review)))
           status (:status review)
           update-fn (fnil inc 0)]
       (update acc date (fnil (fn [m]
                                (update m (cond
                                            (= status "APPROVED") :approved
                                            (= status "REJECTED") :rejected) update-fn))
                              {:approved 0 :rejected 0}))))
   {}
   reviews))

(defn convert-to-list [aggregated-data]
  (map (fn [[date counts]]
         {:date date
          :approved (:approved counts)
          :rejected (:rejected counts)})
       aggregated-data))

(defn convert-reviews [reviews]
  (-> reviews
      aggregate-by-date
      convert-to-list
      sort-reviews-by-date))

(defn button->filter-data-by-day [callback]
  [:> SegmentedControl.Root {:defaultValue "7"
                             :size "1"}
   [:> SegmentedControl.Item {:value "1" :on-click #(callback 1)}
    "24h"]
   [:> SegmentedControl.Item {:value "7" :on-click #(callback 7)}
    "7d"]
   [:> SegmentedControl.Item {:value "14" :on-click #(callback 14)}
    "14d"]
   [:> SegmentedControl.Item {:value "30" :on-click #(callback 30)}
    "30d"]
   [:> SegmentedControl.Item {:value "90" :on-click #(callback 90)}
    "3m"]])

(defn main [reviews]
  (let [reviews-items-map (convert-reviews (-> @reviews :data :results))
        reviews-config {:reviews {:label "Reviews"}
                        :approved {:label "Approved"
                                   :color "hsl(var(--chart-2))"}
                        :rejected {:label "Rejected"
                                   :color "hsl(0 93.5% 81.8%)"}}]
    [:> Section {:size "1" :p "5" :class "bg-white rounded-md"}
     [:> Flex {:flexGrow "1" :direction "column" :align "center" :justify "center"}
      [:> Flex {:mb "5" :width "100%" :justify "between"}
       [:> Box
        [:> Heading {:as "h3" :size "2"}
         "Reviews"]
        [:> Text {:as "label" :color "gray" :weight "light" :size "1"}
         (-> @reviews :data :range-date)]]
       [button->filter-data-by-day (fn [days]
                                     (rf/dispatch [:reports->get-review-data-by-date days]))]]

      (println reviews-items-map)

      (if (empty? reviews-items-map)
        [:> Box {:minHeight "300px" :class "content-center"}
         [:> Text {:as "label" :color "gray" :weight "light" :size "1"}
          "No data found for the selected period"]]
        [charts/chart-container
         {:config reviews-config
          :class-name "h-[300px] w-full"
          :chartid :reviews-chart
          :children
          [:> recharts/BarChart {:accessibilityLayer true
                                 :data reviews-items-map}
           [:> recharts/XAxis {:dataKey "date"
                               :tickLine false
                               :axisLine false
                               :tickMargin 8
                               :hide true}]
           [:> recharts/Tooltip {:content
                                 (fn [props]
                                   (r/as-element
                                    [charts/chart-tooltip-content
                                     (merge
                                      (js->clj props :keywordize-keys true)
                                      {:indicator "line"
                                       :label-formatter (fn [_ payload]
                                                          (.toLocaleDateString
                                                           (new js/Date (-> (first payload)
                                                                            :payload
                                                                            :date))
                                                           "en-US"
                                                           #js{:month "short",
                                                               :day "numeric",
                                                               :year "numeric"}))
                                       :chartid :reviews-chart})]))}]
           [:> recharts/Bar {:dataKey "approved"
                             :fill "var(--color-approved)"
                             :radius 4}]
           [:> recharts/Bar {:dataKey "rejected"
                             :fill "var(--color-rejected)"
                             :radius 4}]]}])]]))
