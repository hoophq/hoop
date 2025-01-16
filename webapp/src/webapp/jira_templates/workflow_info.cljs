(ns webapp.jira-templates.workflow-info
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading Text]]
   [webapp.components.forms :as forms]))

(defn main [{:keys [status on-status-change]}]
  [:> Flex {:direction "column" :gap "5"}
   [:> Box
    [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
     "Workflow transition status"]
    [:> Text {:size "3" :class "text-[--gray-11]"}
     "Define transition status for Jira cards when commands are executed."]]

   [:> Box
    [forms/input
     {:label "Status (Optional)"
      :placeholder "e.g. qa"
      :required false
      :defaultValue @status
      :not-margin-bottom? true
      :on-change #(on-status-change (-> % .-target .-value))}]
    [:> Text {:as "p" :mt "1" :size "2" :class "text-[--gray-10]"}
     "This field is optional and uses Done status as default."]]])
