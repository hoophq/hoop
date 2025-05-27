(ns webapp.jira-templates.loading-jira-templates
  (:require
   ["@radix-ui/themes" :refer [Box Heading Spinner Text]]))

(defn main []
  [:> Box
   [:> Heading {:size "6" :mb "2" :class "text-[--gray-12]"}
    "Loading Jira Template"]
   [:> Text {:as "p" :size "3" :mb "7" :class "text-[--gray-11]"}
    "Loading template data from Jira. If your template includes CMDB fields, this may take longer as we need to fetch additional data from your Jira server."]
   [:> Spinner {:size "3" :class "justify-self-end"}]])
