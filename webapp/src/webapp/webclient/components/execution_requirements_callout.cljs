(ns webapp.webclient.components.execution-requirements-callout
  (:require
   ["@radix-ui/themes" :refer [Box Flex IconButton Text]]
   ["lucide-react" :refer [Info X]]))

(defn main [{:keys [on-dismiss]}]
  [:> Flex {:justify "between" :align "start" :gap "2"
            :class "absolute top-3 right-3 z-50 max-w-[280px] pointer-events-auto p-4 rounded-4 bg-warning-1 shadow-lg"}
   [:> Flex {:gap "2" :align "start"}
    [:> Box {:class "shrink-0 mt-[2px] text-warning-12"}
     [:> Info {:size 16}]]
    [:> Flex {:direction "column" :gap "1"}
     [:> Text {:as "p" :size "2" :weight "bold" :class "text-warning-12"}
      "This resource role requires additional information"]
     [:> Text {:as "p" :size "1" :class "text-warning-12"}
      "Additional information will be requested before completing your execution."]]]
   [:> IconButton {:variant "ghost"
                   :color "gray"
                   :size "1"
                   :type "button"
                   :class "shrink-0"
                   :on-click on-dismiss}
    [:> X {:size 14}]]])
