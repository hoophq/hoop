(ns webapp.jira-templates.basic-info
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading Grid Text]]
   [webapp.components.forms :as forms]))

(defn main [{:keys [name
                    description
                    project-key
                    request-type-id
                    on-name-change
                    on-description-change
                    on-project-key-change
                    on-request-type-id-change]}]
  [:> Flex {:direction "column" :gap "5"}
   [:> Box {:grid-column "span 2 / span 2"}
    [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
     "Integration details"]
    [:> Text {:size "3" :class "text-[--gray-11]"}
     "Used to identify your Jira configuration in your connections."]]

   [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
    [forms/input
     {:label "Name"
      :placeholder "e.g. squad-postgresql"
      :required true
      :value @name
      :on-change #(on-name-change (-> % .-target .-value))}]

    [forms/input
     {:label "Description (Optional)"
      :placeholder "Describe how this templated will be used."
      :required false
      :value @description
      :on-change #(on-description-change (-> % .-target .-value))}]

    [forms/input
     {:label "Project Key"
      :placeholder "e.g. PKEY"
      :required true
      :value @project-key
      :on-change #(on-project-key-change (-> % .-target .-value))}]

    [:div
     [forms/input
      {:label "Request Type ID"
       :placeholder "e.g. 10005"
       :required true
       :value @request-type-id
       :not-margin-bottom? true
       :on-change #(on-request-type-id-change (-> % .-target .-value))}]]]])
