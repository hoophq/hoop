(ns webapp.guardrails.main
  (:require [re-frame.core :as rf]
            ["lucide-react" :refer [Construction]]
            ["@radix-ui/themes" :refer [Box Button Flex Text Heading]]))

(defn panel []
  (let [guardrails-rules-list (rf/subscribe [:guardrails->list])
        ;; data [{:name "Rule 1" :description "This is a rule description"}
        ;;       {:name "Rule 2" :description "This is a rule description"}
        ;;       {:name "Rule 3" :description "This is a rule description"}]
        data []]
    ;; (rf/dispatch [:guardrails->get-all])
    (fn []
      [:div
       [:header {:class "mb-7"}
        [:> Flex {:justify "between" :align "center"}
         [:> Box
          [:> Heading {:size "8" :weight "bold" :as "h1"}
           "Guardrails"]
          [:> Text {:size "5" :class "text-[--gray-11]"}
           "Create custom rules to guide and protect usage within your connections"]]

         (when (seq data)
           [:> Button {:size "3"
                       :variant "solid"
                       :on-click #(rf/dispatch [:navigate :create-guardrail])}
            "Create a new Guardrail"])]]
       (if (empty? data)

         [:> Flex {:height "400px" :direction "column" :gap "5" :class "p-[--space-5]" :align "center" :justify "center"}
          [:> Construction {:size 48}]
          [:> Text {:size "3" :class "text-[--gray-11]"}
           "No Guardrails configured in your Organization yet."]
          [:> Button {:size "3"
                      :variant "solid"
                      :on-click #(rf/dispatch [:navigate :create-guardrail])}
           "Create a new Guardrail"]
          [:> Text {:size "2" :pt "5" :class "text-[--gray-11]" :ml "auto"}
           "Need more information? Check out our Guardrails documentation."]]

         [:> Box
          (for [rules data]
            [:> Box {:class (str "first:rounded-t-lg border-x border-t "
                                 "last:rounded-b-lg bg-white last:border-b border-gray-200 "
                                 "p-[--space-5]")}
             [:> Flex {:justify "between"}
              [:> Box
               [:> Text {:size "4" :weight "bold"} (:name rules)]
               [:> Text {:as "p" :size "3" :class "text-[--gray-11]"} (:description rules)]]
              [:> Button {:variant "soft"
                          :color "gray"
                          :size "3"}
               "Configure"]]])])])))

