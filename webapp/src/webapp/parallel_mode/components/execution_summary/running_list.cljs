(ns webapp.parallel-mode.components.execution-summary.running-list
  (:require
   ["@radix-ui/themes" :refer [Badge Box Flex Text]]
   ["lucide-react" :refer [Loader2]]
   [re-frame.core :as rf]
   [webapp.connections.constants :as connection-constants]))

(defn running-item [exec]
  [:> Box {:class "px-6 py-3 border-b border-gray-3 last:border-b-0"}
   [:> Flex {:justify "between" :align "center" :gap "4"}
    [:> Flex {:align "center" :gap "3"}
     [:img {:src (connection-constants/get-connection-icon exec)
            :class "w-5 h-5"
            :loading "lazy"}]
     [:> Flex {:direction "column"}
      [:> Text {:size "2" :weight "medium" :class "text-gray-12"}
       (:connection-name exec)]
      [:> Text {:size "1" :color "gray"}
       (:subtype exec)]]]

    [:> Badge {:variant "soft"}
     [:> Flex {:align "center" :gap "1"}
      [:> Loader2 {:size 14 :class "animate-spin"}]
      [:> Text {:size "1"} "Loading"]]]]])

(defn main []
  (let [running-items (rf/subscribe [:parallel-mode/running-executions])
        fade-out? (rf/subscribe [:parallel-mode/should-fade-out?])]
    (fn []
      (when (seq @running-items)
        [:> Box {:class (str "transition-all duration-500 "
                             (when @fade-out? "opacity-0 h-0 overflow-hidden"))}
         (for [exec @running-items]
           ^{:key (:connection-name exec)}
           [running-item exec])]))))

