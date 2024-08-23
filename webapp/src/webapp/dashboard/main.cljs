(ns webapp.dashboard.main
  (:require [webapp.components.charts :as charts]
            ["@radix-ui/themes" :refer [Button]]
            ["lucide-react" :refer [Camera]]
            ["recharts" :as recharts]))

(def chartData
  (clj->js
   [{:month "January", :desktop 186, :mobile 80},
    {:month "February", :desktop 305, :mobile 200},
    {:month "March", :desktop 237, :mobile 120},
    {:month "April", :desktop 73, :mobile 190},
    {:month "May", :desktop 209, :mobile 130},
    {:month "June", :desktop 214, :mobile 140}]))

(def chartConfig
  {:desktop {:label "Desktop"
             :color "#2563eb"}
   :mobile {:label "Mobile"
            :color "#60a5fa"}})

(defn main []
  [:div
   [:h1 "Dashboard"]
   [:> Button {:variant "classic"}
    "Edit profile"]
   [:> Camera {:size 32 :fill "black" :stroke "white"}]
   [charts/chart-container
    {:config chartConfig
     :class-name "min-h-[200px] w-full"
     :children [:> recharts/BarChart {:accessibilityLayer true
                                      :data chartData}
                [:> recharts/CartesianGrid {:vertical false}]
                [:> recharts/XAxis {:dataKey "month"
                                    :tickLine false
                                    :tickMargin 10
                                    :axisLine false
                                    :tickFormatter (fn [value] (str (subs value 0 3) "."))}]
                [:> recharts/Tooltip {:content [charts/chart-tooltip-content]}]
                [:> recharts/Legend {:content [charts/chart-legend-content]}]
                [:> recharts/Bar {:dataKey "desktop"
                                  :fill "var(--color-desktop)"
                                  :radius 4}]
                [:> recharts/Bar {:dataKey "mobile"
                                  :fill "var(--color-mobile"
                                  :radius 4}]]}]])
