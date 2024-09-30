(ns webapp.connections.views.create-update-connection.connection-details-form
  (:require ["@radix-ui/themes" :refer [Callout Box Switch Flex Grid Text TextField]]
            ["lucide-react" :refer [Star ArrowUpRight]]))

(defn main []
  [:> Flex {:direction "column" :gap "9" :class "px-20"}
   [:> Grid {:columns "2"}
    [:> Flex {:direction "column"}
     [:> Text {:size "4" :weight "bold"} "Connection information"]
     [:> Text {:size "3"} "Names are used to identify your connection and can't be changed."]]
    [:> Box {:class "space-y-radix-5"}
     [:> TextField.Root {:placeholder "mssql-armadillo-9696"}]
     [:> TextField.Root {:placeholder "Press enter to add a new tag"}]]]
   [:> Grid {:columns "2"}
    [:> Flex {:direction "column"}
     [:> Text {:size "4" :weight "bold"} "Configuration parameters"]
     [:> Text {:size "3"} "Setup how users interact with this connection."]
     [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
      [:> Callout.Icon
       [:> ArrowUpRight {:size 16}]]
      [:> Callout.Text
       "Learn more about Reviews"]]
     [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
      [:> Callout.Icon
       [:> ArrowUpRight {:size 16}]]
      [:> Callout.Text
       "Learn more about AI Data Masking"]]]

    [:> Flex {:direction "column" :gap "7"}
     [:> Box {:class "space-y-radix-5"}
      [:> Flex {:align "center" :gap "5"}
       [:> Switch {:defaultChecked true :size "3"}]
       [:> Box
        [:> Text {:as "h4" :size "3" :weight "medium"} "Reviews"]
        [:> Text {:as "p" :size "2"} (str "Require approval prior to connection execution. "
                                          "Enable Just-in-Time access for 30-minute sessions or Command reviews "
                                          "for individual query approvals.")]
        [:> Callout.Root {:size "2" :mt "4"}
         [:> Callout.Icon
          [:> Star {:size 16}]]
         [:> Callout.Text
          "Enable Command reviews by upgrading your plan."]]]]]
     [:> Box {:class "space-y-radix-5"}
      [:> Flex {:align "center" :gap "5"}
       [:> Switch {:defaultChecked true :size "3"}]
       [:> Box
        [:> Text {:as "h4" :size "3" :weight "medium"} "AI Data Masking"]
        [:> Text {:as "p" :size "2"} (str "Provide an additional layer of security by ensuring "
                                          "sensitive data is masked in query results with AI-powered data masking.")]
        [:> Callout.Root {:size "2" :mt "4"}
         [:> Callout.Icon
          [:> Star {:size 16}]]
         [:> Callout.Text
          "Enable AI Data Masking by upgrading your plan."]]]]]
     [:> Box {:class "space-y-radix-5"}
      [:> Flex {:align "center" :gap "5"}
       [:> Switch {:defaultChecked true :size "3"}]
       [:> Box
        [:> Text {:as "h4" :size "3" :weight "medium"} "Database schema"]
        [:> Text {:as "p" :size "2"} "Show database schema in the Editor section."]]]]]]

   [:> Grid {:columns "2"}
    [:> Flex {:direction "column"}
     [:> Text {:size "4" :weight "bold"} "Access modes"]
     [:> Text {:size "3"} "Setup how users interact with this connection."]]
    [:> Flex {:direction "column" :gap "7"}
     [:> Box {:class "space-y-radix-5"}
      [:> Flex {:align "center" :gap "5"}
       [:> Switch {:defaultChecked true :size "3"}]
       [:> Box
        [:> Text {:as "h4" :size "3" :weight "medium"} "Runbooks"]
        [:> Text {:as "p" :size "2"} "Create templates to automate tasks in your organization. "]]]]
     [:> Box {:class "space-y-radix-5"}
      [:> Flex {:align "center" :gap "5"}
       [:> Switch {:defaultChecked true :size "3"}]
       [:> Box
        [:> Text {:as "h4" :size "3" :weight "medium"} "Native"]
        [:> Text {:as "p" :size "2"} (str "Access from your client of preference using hoop.dev to channel "
                                          "connections using our Desktop App or our Command Line Interface.")]]]]
     [:> Box {:class "space-y-radix-5"}
      [:> Flex {:align "center" :gap "5"}
       [:> Switch {:defaultChecked true :size "3"}]
       [:> Box
        [:> Text {:as "h4" :size "3" :weight "medium"} "Web and one-offs"]
        [:> Text {:as "p" :size "2"} (str "Use hoop.dev's developer portal or our "
                                          "CLI's One-Offs commands directly in your terminal.")]]]]]]])
