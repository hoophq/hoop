(ns webapp.resources.views.setup.resource-name-step
  (:require
   ["@radix-ui/themes" :refer [Box Heading Text Badge Flex]]
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
     [:> Box {:class "max-w-[600px] mx-auto p-8 space-y-8"}
      ;; Header
      [:> Box {:class "space-y-4"}
       [:> Heading {:as "h2" :size "6" :weight "bold" :class "text-[--gray-12]"}
        "Setup your Resource"]
       [:> Text {:as "p" :size "3" :class "text-[--gray-11]"}
        "Complete the following information to setup your Resource."]]

      ;; Resource Type Display
      [:> Box {:class "space-y-3"}
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Resource type"]
       [:> Flex {:gap "3" :align "center" :class "p-4 rounded-lg border border-gray-6 bg-gray-2"}
        (when icon-url
          [:img {:src icon-url
                 :class "w-10 h-10"
                 :alt (or resource-subtype "resource")}])
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
            "Loading...")]
         (when (= resource-type "database")
           [:> Badge {:variant "soft" :color "blue" :size "1"}
            "BETA"])]]]

      ;; Resource Name Input
      [:> Box {:class "space-y-3"}
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "Name"]
       [:> Text {:size "2" :class "text-[--gray-11] mb-3"}
        "Used to identify this Resource in your environment."]
       [forms/input {:label "Resource name"
                     :placeholder "e.g. my-postgres-db"
                     :value resource-name
                     :required true
                     :on-change #(rf/dispatch [:resource-setup->set-resource-name (-> % .-target .-value)])}]]]]))

