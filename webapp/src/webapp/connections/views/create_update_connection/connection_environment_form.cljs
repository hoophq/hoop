(ns webapp.connections.views.create-update-connection.connection-environment-form
  (:require ["@radix-ui/themes" :refer [Box Callout Flex Grid Select Text]]
            ["lucide-react" :refer [ArrowUpRight]]
            [reagent.core :as r]
            [webapp.connections.utilities :as utils]
            [webapp.connections.views.configuration-inputs :as config-inputs]))

(def configs (r/atom (utils/get-config-keys :postgres)))

(defn main []
  [:> Flex {:direction "column" :gap "9" :class "px-20"}
   [:> Grid {:columns "2"}
    [:> Flex {:direction "column"}
     [:> Text {:size "4" :weight "bold"} "Set an agent"]
     [:> Text {:size "3"} "Select an agent to provide a secure interaction with your connection."]
     [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
      [:> Callout.Icon
       [:> ArrowUpRight {:size 16}]]
      [:> Callout.Text
       "Learn more about Agents"]]]

    [:> Flex {:direction "column" :gap "7"}
     [:> Box {:class "space-y-radix-5"}
      [:> Select.Root {:size "3"}
       [:> Select.Trigger {:placeholder "Select one" :class "w-full"}]
       [:> Select.Content
        [:> Select.Group
         [:> Select.Item {:value "default"} "Default"]]]]]]]

   [:> Grid {:columns "2"}
    [:> Flex {:direction "column"}
     [:> Text {:size "4" :weight "bold"} "Database credentials"]
     [:> Text {:size "3"} "Provide your database access information."]]
    [:> Flex {:direction "column" :class "space-y-radix-7"}
     (config-inputs/config-inputs-labeled configs {})]]])
