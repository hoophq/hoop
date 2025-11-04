(ns webapp.resources.views.configure.information-tab
  (:require
   ["@radix-ui/themes" :refer [Box Heading Text Badge Flex Grid]]
   [webapp.connections.constants :as conn-constants]))

(defn main [resource]
  (let [icon-url (conn-constants/get-connection-icon {:type (:type resource)
                                                      :subtype (:subtype resource)}
                                                     "default")]
    [:> Box {:class "p-8 space-y-16"}
     ;; Resource type
     [:> Grid {:columns "7" :gap "7"}
      [:> Box {:grid-column "span 3 / span 3"}
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Resource type"]
       [:> Text {:size "2" :class "text-[--gray-11]"}
        "This is the type of resource configured in your environment."]]

      [:> Flex {:grid-column "span 4 / span 4" :direction "column" :justify "between"
                :class "h-[110px] p-radix-4 rounded-lg border border-gray-3 bg-white"}

       [:> Flex {:gap "3" :align "center" :justify "between"}
        (when icon-url
          [:img {:src icon-url
                 :class "w-6 h-6"
                 :alt (or (:subtype resource) "resource")}])

        (when (= (:type resource) "database")
          [:> Badge {:variant "soft" :color "blue" :size "1"}
           "BETA"])]

       [:> Box
        [:> Text {:size "3" :weight "bold" :class "text-[--gray-12]"}
         (if (:subtype resource)
           (case (:subtype resource)
             "postgres" "PostgreSQL"
             "mysql" "MySQL"
             "mongodb" "MongoDB"
             "mssql" "Microsoft SQL Server"
             "oracledb" "Oracle Database"
             "ssh" "SSH"
             "tcp" "TCP"
             "httpproxy" "HTTP Proxy"
             (:subtype resource))
           "Unknown")]]]]

     ;; Resource Name
     [:> Grid {:columns "7" :gap "7"}
      [:> Box {:grid-column "span 3 / span 3"}
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Name"]
       [:> Text {:size "2" :class "text-[--gray-11]"}
        "Used to identify this Resource in your environment."]]

      [:> Box {:grid-column "span 4 / span 4"}
       [:> Box {:class "p-radix-4 rounded-lg border border-gray-3 bg-gray-2"}
        [:> Text {:size "2" :class "text-[--gray-12]"}
         (:name resource)]]]]

     ;; Agent (if available)
     (when (:agent_name resource)
       [:> Grid {:columns "7" :gap "7"}
        [:> Box {:grid-column "span 3 / span 3"}
         [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
          "Agent"]
         [:> Text {:size "2" :class "text-[--gray-11]"}
          "The agent managing this resource."]]

        [:> Box {:grid-column "span 4 / span 4"}
         [:> Box {:class "p-radix-4 rounded-lg border border-gray-3 bg-gray-2"}
          [:> Text {:size "2" :class "text-[--gray-12]"}
           (:agent_name resource)]]]])]))

