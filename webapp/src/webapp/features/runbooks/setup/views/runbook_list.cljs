(ns webapp.features.runbooks.setup.views.runbook-list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   [re-frame.core :as rf]))


(defn main []
  (let [runbooks-rules-list (rf/subscribe [:runbooks-rules/list])]

    (fn []
      (let [rules-data (or (:data @runbooks-rules-list) [])]
        [:> Box {:class "w-full h-full"}
         [:> Box {:class "min-h-full h-max"}
          (doall
           (for [rule rules-data]
             ^{:key (:id rule)}
             [:> Box {:class (str "first:rounded-t-lg border-x border-t "
                                  "last:rounded-b-lg bg-white last:border-b border-gray-200 "
                                  "p-[--space-5]")}
              [:> Flex {:justify "between" :align "center"}
               [:> Box
                [:> Text {:size "4" :weight "bold"} (or (:name rule) (:id rule) "Unnamed Rule")]
                [:> Text {:as "p" :size "3" :class "text-[--gray-11]"}
                 (or (:description rule) (str rule))]]
               [:> Button {:variant "soft"
                           :color "gray"
                           :size "3"
                           :on-click #(rf/dispatch [:navigate :edit-runbooks-rule {} :rule-id (:id rule)])}
                "Configure"]]]))]]))))
