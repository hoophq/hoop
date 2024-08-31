(ns webapp.dashboard.redacted-data-chart
  (:require ["@radix-ui/themes" :refer [Box Flex Heading Section
                                        SegmentedControl Text]]
            ["recharts" :as recharts]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.charts :as charts]))

(defn formatted [s]
  (-> s
      (cs/replace "_" "-")
      (cs/lower-case)))

(defn color-fn
  [i] (str "hsl(var(--chart-" (inc i) "))"))

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

(defn main [redacted-data]
  (let [redact-data-items (-> @redacted-data :data :items)
        redata-items-map (reduce (fn [acc {:keys [info_type redact_total]}]
                                   (let [formatted-info-type (formatted info_type)
                                         existing (some #(when (= (:info_type %) formatted-info-type) %) acc)]
                                     (if existing
                                       (mapv (fn [m]
                                               (if (= (:info_type m) formatted-info-type)
                                                 (update m :redact_total + redact_total)
                                                 m))
                                             acc)
                                       (conj acc {:info_type formatted-info-type
                                                  :redact_total redact_total
                                                  :fill (str "var(--color-" (cs/replace
                                                                             formatted-info-type
                                                                             " " "-") ")")}))))
                                 []
                                 redact-data-items)
        config-redact-data (merge
                            (reduce (fn [acc {:keys [info_type _]}]
                                      (let [idx (count acc)]
                                        (assoc acc
                                               (keyword (cs/replace (cs/lower-case info_type) " " "-"))
                                               {:label info_type
                                                :color (color-fn idx)})))
                                    {}
                                    redata-items-map))]
    [:> Section {:size "1"}
     [:> Box {:p "5" :class "bg-white rounded-md"}
      [:> Flex {:flexGrow "1" :direction "column" :align "center" :justify "center"}
       [:> Flex {:width "100%" :justify "between"}
        [:> Box
         [:> Heading {:as "h3" :size "2"}
          "Redacted Data"]
         [:> Text {:as "label" :color "gray" :weight "light" :size "1"}
          (-> @redacted-data :data :range-date)]]

        [button->filter-data-by-day (fn [days]
                                      (rf/dispatch [:reports->get-redact-data-by-date days]))]]

       (if (empty? redata-items-map)
         [:> Box {:minHeight "246px" :class "content-center"}
          [:> Text {:as "label" :color "gray" :weight "light" :size "1"}
           "No data found for the selected period"]]
         [charts/chart-container
          {:config config-redact-data
           :class-name "max-h-[400px] w-full"
           :chartid :redact-chart
           :children [:> recharts/BarChart {:accessibilityLayer true
                                            :data (clj->js redata-items-map)}
                      [:> recharts/Tooltip {:content (fn [props]
                                                       (r/as-element
                                                        [charts/chart-tooltip-content
                                                         (merge
                                                          (js->clj props :keywordize-keys true)
                                                          {:name-key (-> (js->clj props :keywordize-keys true)
                                                                         :payload
                                                                         first
                                                                         :payload
                                                                         :info_type)
                                                           :indicator "line"
                                                           :chartid :redact-chart})]))}]
                      [:> recharts/Bar {:dataKey "redact_total"
                                        :radius 4}]]}])]]]))
