(ns webapp.features.ai-session-analyzer.views.ai-analyzer-card
  (:require
   ["@radix-ui/themes" :refer [Flex Spinner Text]]))

(defn ai-analyzer-card []
  [:> Flex {:justify "between" :align "start" :gap "3"
            :class "absolute bottom-3 right-3 items-center z-50 max-w-[400px] pointer-events-auto p-6 rounded-6 bg-[--gray-1] shadow-lg"}
   [:> Flex {:align "center" :gap "3"}
    [:img {:src "/images/ai-session-analyzer-logo.svg"
           :class "w-10 h-10 shrink-0"
           :alt "AI Session Analyzer"}]
    [:> Flex {:direction "column" :gap "1"}
     [:> Text {:size "3" :weight "bold" :class "text-[--gray-12]"}
      "AI Session Analyzer is thinking..."]
     [:> Text {:size "2" :class "text-[--gray-11]"}
      "This may take a few moments."]]]
   [:> Spinner {:size "2" :class "text-[--gray-12] ml-auto"}]])
