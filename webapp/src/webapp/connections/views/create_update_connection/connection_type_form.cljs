(ns webapp.connections.views.create-update-connection.connection-type-form
  (:require ["@radix-ui/themes" :refer [Avatar RadioGroup Box Card Flex Grid Text]]
            ["lucide-react" :refer [Database SquareTerminal Workflow AppWindow]]
            [reagent.core :as r]))

(def connections-type
  [{:icon (r/as-element [:> Database {:size 16}])
    :title "Database"
    :subtitle "For PostgreSQL, MySQL, Microsoft SQL and more."
    :value :database}
   {:icon (r/as-element [:> SquareTerminal {:size 16}])
    :title "Shell"
    :subtitle "Custom connection for your services."
    :value :custom}
   {:icon (r/as-element [:> SquareTerminal {:size 16}])
    :title "SSH"
    :subtitle "Secure shell protocol for remote access."
    :value :ssh}
   {:icon (r/as-element [:> Workflow {:size 16}])
    :title "TCP"
    :subtitle "Transmission protocol for reliable transmission of data."
    :value :application}
   {:icon (r/as-element [:> AppWindow {:size 16}])
    :title "Application"
    :subtitle "For Ruby on Rails, Python, Node JS and more."
    :value :application}])

(defn main [connection-type connection-subtype]
  [:> Flex {:direction "column" :gap "9" :class "px-20"}
   [:> Grid {:columns "2"}
    [:> Flex {:direction "column"}
     [:> Text {:size "4" :weight "bold"} "Connection type"]
     [:> Text {:size "3"} "Select the type of connection for your service."]]
    [:> Box {:class "space-y-radix-5"}
     (for [{:keys [icon title subtitle value]} connections-type]
       ^{:key title}
       [:> Card {:size "1" :class "w-full cursor-pointer" :on-click #(reset! connection-type value)}
        [:> Flex {:align "center" :gap "3"}
         [:> Avatar {:size "4"
                     :variant "soft"
                     :color "gray"
                     :fallback icon}]
         [:> Flex {:direction "column"}
          [:> Text {:size "3" :weight "medium" :color "gray-12" :class "dark"} title]
          [:> Text {:size "2"} subtitle]]]])]]
   [:> Grid {:columns "2"}
    [:> Flex {:direction "column"}
     [:> Text {:size "4" :weight "bold"} "Database type"]
     [:> Text {:size "3"} "Select the type of database for your connection."]]
    [:> Box {:class "space-y-radix-5"}
     [:> RadioGroup.Root {:name "database-type" :class "space-y-radix-4"}
      [:> RadioGroup.Item {:value "postgres"} "PostgreSQL"]
      [:> RadioGroup.Item {:value "mysql"} "MySQL"]
      [:> RadioGroup.Item {:value "mssql"} "Microsoft SQL"]
      [:> RadioGroup.Item {:value "oracledb"} "Oracle DB"]
      [:> RadioGroup.Item {:value "mongodb"} "MongoDB"]]]]])
