(ns webapp.resources.views.setup.resource-name-step
  (:require
   ["@radix-ui/themes" :refer [Box Heading Text Badge Flex Grid]]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.connections.constants :as conn-constants]))

(defn main []
  (let [resource-name @(rf/subscribe [:resource-setup/resource-name])
        resource-type @(rf/subscribe [:resource-setup/resource-type])
        resource-subtype @(rf/subscribe [:resource-setup/resource-subtype])
        _ (js/console.log "📋 Resource Name Step - type:" resource-type "subtype:" resource-subtype)
        icon-url (conn-constants/get-connection-icon {:type resource-type
                                                      :subtype resource-subtype}
                                                     "default")
        _ (js/console.log "🎨 Icon URL:" icon-url)]
    [:form {:id "resource-name-form"
            :on-submit (fn [e]
                         (.preventDefault e)
                         (rf/dispatch [:resource-setup->next-step :agent-selector]))}
     [:> Box {:class "p-8 space-y-16"}
      ;; Header
      [:> Box
       [:> Heading {:as "h2" :size "6" :weight "bold" :class "text-gray-12"}
        "Setup your Resource"]
       [:> Text {:as "p" :size "3" :class "text-gray-12"}
        "Complete the following information to setup your Resource."]]

      [:> Grid {:columns "7" :gap "7"}
       [:> Box {:grid-column "span 3 / span 3"}
        [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
         "Resource type"]
        [:> Text {:size "2" :class "text-[--gray-11]"}
         "This name is used to identify your Agent in your environment."]]

       [:> Flex {:grid-column "span 4 / span 4" :direction "column" :justify "between"
                 :class "h-[110px] p-radix-4 rounded-lg border border-gray-3 bg-white"}

        [:> Flex {:gap "3" :align "center" :justify "between"}
         (when icon-url
           [:img {:src icon-url
                  :class "w-6 h-6"
                  :alt (or resource-subtype "resource")}])

         (when (= resource-type "database")
           [:> Badge {:variant "soft" :color "blue" :size "1"}
            "BETA"])]

        [:> Box
         [:> Text {:size "3" :weight "bold" :class "text-[--gray-12]"}
          (if resource-subtype
            (case resource-subtype
              "postgres" "PostgreSQL"
              "mysql" "MySQL"
              "mongodb" "MongoDB"
              "mssql" "Microsoft SQL Server"
              "oracledb" "Oracle Database"
              "ssh" "SSH"
              "tcp" "TCP"
              "httpproxy" "HTTP Proxy"
              resource-subtype)
            "Loading...")]]]]

      ;; Resource Name Input
      [:> Grid {:columns "7" :gap "7"}
       [:> Box {:grid-column "span 3 / span 3"}
        [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
         "Name"]
        [:> Text {:size "2" :class "text-[--gray-11]"}
         "Used to identify this Resource in your environment."]]

       [:> Box {:grid-column "span 4 / span 4"}
        [forms/input {:label "Resource name"
                      :placeholder "e.g. my-postgres-db"
                      :value resource-name
                      :required true
                      :on-change #(rf/dispatch [:resource-setup->set-resource-name (-> % .-target .-value)])}]]]]]))

