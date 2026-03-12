(ns webapp.features.ai-session-analyzer.views.rule-list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   [re-frame.core :as rf]))

(defn main []
  (let [rules-data (rf/subscribe [:ai-session-analyzer/rules])]
    (fn []
      (let [rules (or (:data @rules-data) [])]
        [:> Box {:class "w-full h-full"}
         [:> Box {:class "min-h-full h-max"}
          (doall
           (for [rule rules]
             ^{:key (:id rule)}
             [:> Box {:class (str "first:rounded-t-lg border-x border-t "
                                  "last:rounded-b-lg bg-white last:border-b border-gray-200 "
                                  "p-[--space-5]")}
              [:> Flex {:justify "between" :align "center"}
               [:> Box
                [:> Text {:size "4" :weight "bold"} (or (:name rule) "Unnamed Rule")]
                [:> Text {:as "p" :size "3" :class "text-[--gray-11]"}
                 (or (:description rule) "")]]
               [:> Button {:variant "soft"
                           :color "gray"
                           :size "3"
                           :on-click #(rf/dispatch [:navigate :edit-ai-session-analyzer-rule {} :rule-name (:name rule)])}
                "Configure"]]]))]]))))
