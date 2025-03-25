(ns webapp.jira-templates.loading-jira-templates
  (:require
   ["@radix-ui/themes" :refer [Box Heading Spinner Text]]))

(defn main []
  [:> Box
   [:> Heading {:size "6" :mb "2" :class "text-[--gray-12]"}
    "Verifying Jira Templates"]
   [:> Text {:as "p" :size "3" :mb "7" :class "text-[--gray-11]"}
    (str "This connection has additional verification for Jira Templates "
         "and might take a few seconds before proceeding. Please wait until "
         "the verification is processed without closing this tab.")]
   [:> Spinner {:size "3" :class "justify-self-end"}]])
