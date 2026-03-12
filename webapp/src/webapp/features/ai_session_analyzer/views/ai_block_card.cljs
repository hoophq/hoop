(ns webapp.features.ai-session-analyzer.views.ai-block-card
  (:require
   ["@radix-ui/themes" :refer [Badge Flex Text]]
   ["lucide-react" :refer [X]]
   [reagent.core :as r]))

(defn ai-block-card [{:keys [title explanation]}]
  (let [dismissed? (r/atom false)]
    (fn []
      (when-not @dismissed?
        [:> Flex {:justify "between" :align "start" :gap "3"
                  :class "absolute bottom-3 right-3 z-50 max-w-[523px] pointer-events-auto p-6 rounded-6 bg-[--gray-1] shadow-lg"}
         [:> Flex {:align "start" :gap "3"}
          [:img {:src "/images/ai-session-analyzer-logo.svg"
                 :class "w-10 h-10 shrink-0"
                 :alt "AI Session Analyzer"}]
          [:> Flex {:direction "column" :gap "2"}
           [:> Flex {:align "center" :justify "between" :gap "2" :wrap "wrap"}
            [:> Text {:size "3" :weight "bold" :class "text-[--gray-12]"}
             title]
            [:> Badge {:color "red"}
             "Action Blocked"]]
           [:> Text {:size "2" :class "text-[--gray-12]"}
            explanation]]]
         [:> X {:size 16
                :class "shrink-0 cursor-pointer text-[--gray-11] hover:text-[--gray-12] mt-0.5"
                :on-click #(reset! dismissed? true)}]]))))
