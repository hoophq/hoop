(ns webapp.dashboard.connection-chart
  (:require ["@radix-ui/themes" :refer [Box Flex Heading Section Text]]
            ["recharts" :as recharts]
            [clojure.string :as cs]
            [reagent.core :as r]
            [webapp.components.charts :as charts]))

(defn color-fn
  [i] (str "hsl(var(--chart-" (inc i) "))"))

(defn aggregate-subtypes [connections]
  (reduce
   (fn [acc connection]
     (let [subtype (:subtype connection)]
       (if (empty? subtype)
         (update acc "others" (fnil inc 0))
         (update acc subtype (fnil inc 0)))))
   {}
   connections))

(defn connection-convert-to-list [aggregated-data]
  (map-indexed
   (fn [i [subtype amount]]
     {:type subtype
      :amount amount
      :fill (color-fn i)})
   aggregated-data))

(defn convert-connections [connections]
  (-> connections
      aggregate-subtypes
      connection-convert-to-list))

(defn main [connections]
  (let [connection-items-map (convert-connections (:results @connections))
        connection-items-config (merge
                                 (reduce (fn [acc {:keys [type _]}]
                                           (let [idx (count acc)]
                                             (assoc acc
                                                    (keyword (cs/replace (cs/lower-case type) " " "-"))
                                                    {:label type
                                                     :color (color-fn idx)})))
                                         {}
                                         connection-items-map))]
    [:> Section {:size "1" :p "5" :class "bg-white rounded-md"}
     [:> Flex {:flexGrow "1" :direction "column" :align "center" :justify "center"}
      [:> Flex {:width "100%"}
       [:> Box {:mb "5"}
        [:> Heading {:as "h3" :size "2"}
         "Connections"]]]

      (if (empty? connection-items-map)
        [:> Box {:minHeight "300px" :class "content-center"}
         [:> Text {:as "label" :color "gray" :weight "light" :size "1"}
          "No data found for the selected period"]]

        [charts/chart-container
         {:config connection-items-config
          :class-name "mx-auto w-full aspect-square h-[300px]"
          :chartid :connections-chart
          :children [:> recharts/PieChart
                     [:> recharts/Tooltip {:content (fn [props]
                                                      (r/as-element
                                                       [charts/chart-tooltip-content
                                                        (merge
                                                         (js->clj props :keywordize-keys true)
                                                         {:indicator "line"
                                                          :hide-label true
                                                          :chartid :connections-chart})]))
                                           :cursor false}]
                     [:> recharts/Pie
                      {:data connection-items-map
                       :dataKey "amount"
                       :nameKey "type"
                       :innerRadius 60
                       :strokeWidth 5}]]}])]]))
