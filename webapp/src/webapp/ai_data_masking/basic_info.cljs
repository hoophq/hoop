(ns webapp.ai-data-masking.basic-info
  (:require
   ["@radix-ui/themes" :refer [Box Flex Grid Heading Text]]
   [webapp.components.forms :as forms]))

(defn main [{:keys [name
                    description
                    score_threshold
                    on-name-change
                    on-description-change
                    on-score-threshold-change]}]
  [:> Grid {:columns "7" :gap "7"}
   [:> Box {:grid-column "span 2 / span 2"}
    [:> Flex {:align "center" :gap "2"}
     [:> Heading {:as "h3" :size "4" :weight "medium"} "Set rule information"]]
    [:> Text {:size "3" :class "text-[--gray-11]"}
     "Used to identify the rule in your resource roles."]]

   [:> Box {:class "space-y-radix-5" :grid-column "span 5 / span 5"}
    [:> Box
     [forms/input
      {:label "Name"
       :name "name"
       :value @name
       :on-change #(on-name-change (-> % .-target .-value))
       :placeholder "e.g. Sensitive Data"
       :required true}]]

    [:> Box
     [forms/textarea
      {:label "Description"
       :label-suffix "(Optional)"
       :name "description"
       :value @description
       :on-change #(on-description-change (-> % .-target .-value))
       :placeholder "Describe how this is used in your resource roles"
       :rows 3}]]

    [:> Box
     [forms/input
      {:label "Analyzer confidence threshold"
       :label-suffix "(Optional)"
       :name "score_threshold"
       :type "number"
       :min 1
       :max 100
       :maxlength 3
       :value @score_threshold
       :on-change #(let [value (-> % .-target .-value)]
                     (on-score-threshold-change (if (empty? value) nil (js/parseInt value))))
       :placeholder "85"}]
     [:> Text {:size "2" :class "text-[--gray-11] mt-1"}
      "Minimum confidence level required to detect and mask sensitive data. Default 85% works well for most use cases."]]]])
