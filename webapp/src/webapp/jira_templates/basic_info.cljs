(ns webapp.jira-templates.basic-info
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading Grid Text]]
   [webapp.components.forms :as forms]))

(defn main [{:keys [name
                    description
                    project-key
                    issue-type
                    on-name-change
                    on-description-change
                    on-project-key-change
                    on-issue-type-change]}]
  [:> Flex {:direction "column" :gap "5"}
   [:> Box {:grid-column "span 2 / span 2"}
    [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
     "Integration details"]
    [:> Text {:size "3" :class "text-[--gray-11]"}
     "Used to identify your Jira configuration in your connections."]]

   [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
    [forms/input
     {:label "Name"
      :placeholder "banking+prod_mongpdb"
      :required true
      :value @name
      :on-change #(on-name-change (-> % .-target .-value))}]

    [forms/input
     {:label "Description (Optional)"
      :placeholder "Describe how this is used in your connections"
      :required false
      :value @description
      :on-change #(on-description-change (-> % .-target .-value))}]

    [forms/input
     {:label "Project Key"
      :placeholder "PKEY"
      :required true
      :value @project-key
      :on-change #(on-project-key-change (-> % .-target .-value))}]

    [:div
     [forms/input
      {:label "Request Type"
       :placeholder "Name"
       :required true
       :value @issue-type
       :not-margin-bottom? true
       :on-change #(on-issue-type-change (-> % .-target .-value))}]
     [:> Text {:as "p" :size "2" :mt "1" :class "text-[--gray-10]"}
      "You can find the name under your Project Settings."]]]])
