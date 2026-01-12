(ns webapp.parallel-mode.components.execution-summary.success-list
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Flex Text]]
   ["lucide-react" :refer [Check ExternalLink]]
   [re-frame.core :as rf]
   [webapp.connections.constants :as connection-constants]))

(defn success-item [exec]
  [:> Box {:class "px-6 py-3 border-b border-gray-3 last:border-b-0 hover:bg-gray-2 transition-colors"}
   [:> Flex {:justify "between" :align "center" :gap "4"}
    [:> Flex {:align "center" :gap "3" :class "flex-1"}
     [:img {:src (connection-constants/get-connection-icon exec)
            :class "w-5 h-5"
            :loading "lazy"}]
     [:> Flex {:direction "column"}
      [:> Text {:size "2" :weight "medium" :class "text-gray-12"}
       (:connection-name exec)]
      [:> Text {:size "1" :color "gray"}
       (:subtype exec)]]]

    [:> Flex {:align "center" :gap "4"}
     ;; Status badge
     (when (= (:status exec) :waiting-review)
       [:> Badge {:variant "soft" :color "yellow"}
        [:> Flex {:align "center" :gap "1"}
         [:> Check {:size 14}]
         [:> Text {:size "1"} "Approval Required"]]])


     ;; Open button
     [:a {:href (str (.. js/window -location -origin) "/sessions/" (:session-id exec))
          :target "_blank"
          :rel "noopener noreferrer"}
      [:> Button {:size "2" :variant "soft"}
       "Open"
       [:> ExternalLink {:size 14 :class "ml-1"}]]]]]])

(defn main []
  (let [success-items (rf/subscribe [:parallel-mode/success-executions])]
    (fn []
      (if (seq @success-items)
        [:> Box
         (for [exec @success-items]
           ^{:key (:connection-name exec)}
           [success-item exec])]

        [:> Flex {:direction "column" :align "center" :justify "center" :class "py-12"}
         [:> Text {:size "2" :color "gray"}
          "No successful executions yet"]]))))

