(ns webapp.ai-data-masking.basic-info
  (:require
   ["@radix-ui/themes" :refer [Box Flex Grid Heading Text]]
   [webapp.components.forms :as forms]))

(defn main [{:keys [name
                    description
                    on-name-change
                    on-description-change]}]
  [:> Grid {:columns "7" :gap "7"}
   [:> Box {:grid-column "span 2 / span 2"}
    [:> Flex {:align "center" :gap "2"}
     [:> Heading {:as "h3" :size "4" :weight "medium"} "Set rule information"]]
    [:> Text {:size "3" :class "text-[--gray-11]"}
     "Used to identify the rule in your connections."]]

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
       :placeholder "Describe how this is used in your connections"
       :rows 3}]]]])
