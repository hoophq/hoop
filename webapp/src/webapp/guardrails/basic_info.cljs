(ns webapp.guardrails.basic-info
  (:require
   ["@radix-ui/themes" :refer [Box Grid]]
   [webapp.components.forms :as forms]))

(defn main [{:keys [name description on-name-change on-description-change]}]
  [:> Grid {:columns "7" :gap "7"}
   [:> Box {:grid-column "span 2 / span 2"}
    [:h3 {:class "text-sm font-semibold mb-2"} "Set Guardrail information"]
    [:p {:class "text-sm text-gray-500"} "Used to identify your Guardrail in your connections."]]

   [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
    [forms/input
     {:label "Name"
      :placeholder "Sensitive Data"
      :required true
      :value @name
      :on-change #(on-name-change (-> % .-target .-value))}]
    [forms/input
     {:label "Description (Optional)"
      :placeholder "Describe how this is used in your connections"
      :required false
      :value @description
      :on-change #(on-description-change (-> % .-target .-value))}]]])
