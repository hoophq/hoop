(ns webapp.parallel-mode.components.execution-summary.error-list
  (:require
   ["@radix-ui/themes" :refer [Badge Box Flex Text]]
   ["lucide-react" :refer [AlertTriangle Clock X]]
   [re-frame.core :as rf]
   [webapp.connections.constants :as connection-constants]))

(defn get-error-badge [exec]
  (case (:status exec)
    :error-jira-template
    [:> Badge {:variant "soft" :color "yellow"}
     [:> Flex {:align "center" :gap "1"}
      [:> Clock {:size 14}]
      [:> Text {:size "1"} "Jira Template not allowed in Parallel Mode"]]]

    :error-review-required
    [:> Badge {:variant "soft" :color "blue"}
     [:> Flex {:align "center" :gap "1"}
      [:> Clock {:size 14}]
      [:> Text {:size "1"} "Approval Required"]]]

    :error-metadata-required
    [:> Badge {:variant "soft" :color "yellow"}
     [:> Flex {:align "center" :gap "1"}
      [:> AlertTriangle {:size 14}]
      [:> Text {:size "1"} "Required Metadata not allowed in Parallel Mode"]]]

    :cancelled
    [:> Badge {:variant "soft" :color "gray"}
     [:> Flex {:align "center" :gap "1"}
      [:> X {:size 14}]
      [:> Text {:size "1"} "Cancelled"]]]

    ;; Default error
    [:> Badge {:variant "soft" :color "red"}
     [:> Flex {:align "center" :gap "1"}
      [:> AlertTriangle {:size 14}]
      [:> Text {:size "1"} "Error"]]]))

(defn error-item [exec]
  [:> Box {:class "px-6 py-3 border-b border-gray-3 last:border-b-0 hover:bg-gray-2 transition-colors"}
   [:> Flex {:direction "column" :gap "2"}
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

     [get-error-badge exec]]]])

(defn main []
  (let [error-items (rf/subscribe [:parallel-mode/error-executions])]
    (fn []
      (if (seq @error-items)
        [:> Box
         (for [exec @error-items]
           ^{:key (:connection-name exec)}
           [error-item exec])]

        [:> Flex {:direction "column" :align "center" :justify "center" :class "py-12"}
         [:> Text {:size "2" :color "gray"}
          "No errors"]]))))

