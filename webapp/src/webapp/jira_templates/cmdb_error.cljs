(ns webapp.jira-templates.cmdb-error
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text]]))

(defn main [{:keys [on-retry on-cancel]}]
  [:> Box
   [:> Heading {:size "6" :mb "2" :class "text-[--gray-12]"}
    "Jira CMDB Connection Issue"]
   [:> Text {:as "p" :size "3" :mb "4" :class "text-[--gray-11]"}
    "We couldn't retrieve some required CMDB data from your Jira server. This template requires valid CMDB fields to continue."]
   [:> Text {:as "p" :size "2" :mb "7" :class "text-[--orange-11]"}
    "This is usually due to Jira server availability or permission issues. Please contact your Jira administrator to ensure the CMDB service is functioning properly."]
   [:> Flex {:gap "3" :justify "end"}
    [:> Button {:variant "soft" :on-click on-cancel} "Cancel"]
    [:> Button {:variant "solid" :on-click on-retry} "Try again"]]])
