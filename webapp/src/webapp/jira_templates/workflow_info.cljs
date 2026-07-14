(ns webapp.jira-templates.workflow-info
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading Switch Text]]
   [webapp.components.forms :as forms]))

(defn main [{:keys [status
                    on-status-change
                    skip-transition-on-nonzero-exit-code
                    on-skip-transition-change]}]
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
      :value @status
      :not-margin-bottom? true
      :on-change #(on-status-change (-> % .-target .-value))}]
    [:> Text {:as "p" :mt "1" :size "2" :class "text-[--gray-10]"}
     "This field is case insensitive and uses 'done' status as default."]]

   [:> Flex {:align "center" :gap "5"}
    [:> Switch {:checked @skip-transition-on-nonzero-exit-code
                :size "3"
                :aria-label "Skip transition on failed sessions"
                :onCheckedChange #(on-skip-transition-change %)}]
    [:> Box
     [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12]"}
      "Skip transition on failed sessions"]
     [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
      "Do not transition the Jira issue when the session finishes with a non-zero exit code."]]]])
