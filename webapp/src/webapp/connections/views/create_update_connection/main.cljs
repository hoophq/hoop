(ns webapp.connections.views.create-update-connection.main
  (:require ["@radix-ui/themes" :refer [Box Button Flex Heading Text]]
            ["lucide-react" :refer [BadgeInfo GlobeLock SquareStack]]
            [reagent.core :as r]
            [webapp.components.accordion :as accordion]
            [webapp.connections.views.create-update-connection.connection-details-form :as connection-details-form]
            [webapp.connections.views.create-update-connection.connection-environment-form :as connection-environment-form]
            [webapp.connections.views.create-update-connection.connection-type-form :as connection-type-form]))

(defn main []
  (let [connection-type (r/atom nil)
        connection-subtype (r/atom "")]
    (fn []
      [:Flex {:direction "column" :gap "5"}
       [:> Flex {:justify "between" :py "5" :mb "7"}
        [:> Box
         [:> Heading {:size "8" :as "h1"} "Create Connection"]
         [:> Text {:size "5"} "Setup a secure access to your resources."]]
        [:> Button {:size "4"} "Save and Confirm"]]

       [:> Box {:class "space-y-radix-5"}
        [accordion/main [{:title "Choose your resource type"
                          :subtitle "Connections can be created for databases, applications and more."
                          :value "resource-type"
                          :avatar-icon [:> SquareStack {:size 16}]
                          :content [connection-type-form/main connection-type connection-subtype]}]]

        [accordion/main [{:title "Define connection details"
                          :subtitle "Setup how do you want to identify the connection and additional configuration parameters."
                          :value "connection-details"
                          :avatar-icon [:> BadgeInfo {:size 16}]
                          :content [connection-details-form/main]}]]

        [accordion/main [{:title "Environment setup"
                          :subtitle "Setup your environment information to establish a secure connection."
                          :value "environment-setup"
                          :avatar-icon [:> GlobeLock {:size 16}]
                          :content [connection-environment-form/main]}]]]])))
