(ns webapp.connections.views.create-update-connection.connection-advance-settings-form
  (:require
   ["@radix-ui/themes" :refer [Box Callout Flex Grid Link Switch Text]]
   ["lucide-react" :refer [ArrowUpRight]]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multi-select]
   [webapp.routes :as routes]))


(defn access-mode-exec-disabled? [connection-type connection-subtype]
  (cond
    (and (= connection-type "application")
         (= connection-subtype "tcp")) true
    (and (= connection-type "custom")
         (= connection-subtype "ssh")) true
    :else false))

(defn access-mode-connect-disabled? [connection-type connection-subtype]
  (cond
    (and (= connection-type "database")
         (= connection-subtype "oracledb")) true
    :else false))

(defn access-mode-runbooks-disabled? [connection-type connection-subtype]
  (cond
    (and (= connection-type "application")
         (= connection-subtype "tcp")) true
    (and (= connection-type "custom")
         (= connection-subtype "ssh")) true
    :else false))

(defn main
  [{:keys [connection-type
           connection-subtype
           connection-tags-value
           connection-tags-input-value
           enable-database-schema
           access-mode-runbooks
           access-mode-exec
           access-mode-connect
           guardrails-options
           guardrails
           jira-templates-options
           jira-template-id]}]
  [:> Flex {:direction "column" :gap "9" :class "px-20"}
   [:> Grid {:columns "5" :gap "7"}
    [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
     [:> Text {:size "4" :weight "bold" :class "text-gray-12"} "Additional information"]
     [:> Text {:size "3" :class "text-gray-11"} "Categorize your connections with tags."]]
    [:> Box {:class "space-y-radix-5" :grid-column "span 3 / span 3"}
     [multi-select/text-input {:value @connection-tags-value
                               :input-value @connection-tags-input-value
                               :on-change (fn [value]
                                            (reset! connection-tags-value value))
                               :on-input-change (fn [value]
                                                  (reset! connection-tags-input-value value))
                               :label "Tags"
                               :id "tags-multi-select-text-input"
                               :name "tags-multi-select-text-input"}]]]

   [:> Grid {:columns "5" :gap "7"}
    [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
     [:> Text {:size "4" :weight "bold" :class "text-gray-12"} "Guardrails"]
     [:> Text {:size "3" :class "text-gray-11"} "Create custom rules to guide and protect usage within your connections."]
     [:> Link {:href (routes/url-for :guardrails)
               :on-click #(rf/dispatch [:modal->close])}
      [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
       [:> Callout.Icon
        [:> ArrowUpRight {:size 16}]]
       [:> Callout.Text
        "Go to Guardrails"]]]]
    [:> Box {:class "space-y-radix-5" :grid-column "span 3 / span 3"}
     [multi-select/main {:options guardrails-options
                         :id "guardrails-input"
                         :name "guardrails-input"
                         :default-value (or @guardrails [])
                         :on-change #(reset! guardrails (js->clj %))}]]]

   [:> Grid {:columns "5" :gap "7"}
    [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
     [:> Text {:size "4" :weight "bold" :class "text-gray-12"} "Jira templates"]
     [:> Text {:size "3" :class "text-gray-11"} "Optimize and automate workflows with Jira Integration."]
     [:> Link {:href (routes/url-for :jira-templates)
               :on-click #(rf/dispatch [:modal->close])}
      [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
       [:> Callout.Icon
        [:> ArrowUpRight {:size 16}]]
       [:> Callout.Text
        "Go to JIRA Integration"]]]]
    [:> Box {:class "space-y-radix-5" :grid-column "span 3 / span 3"}
     [forms/select {:placeholder "Select one"
                    :full-width? true
                    :class "w-full"
                    :options jira-templates-options
                    :id "jira-template-select"
                    :name "jira-template-select"
                    :selected @jira-template-id
                    :on-change #(reset! jira-template-id (js->clj %))}]]]

   (when (= "database" @connection-type)
     [:> Grid {:columns "5" :gap "7"}
      [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
       [:> Text {:size "4" :weight "bold" :class "text-gray-12"} "Data visualization"]
       [:> Text {:size "3" :class "text-gray-11"} "Available to specific connection types only."]]
      [:> Flex {:direction "column" :gap "7" :grid-column "span 3 / span 3"}
       [:> Box {:class "space-y-radix-5"}
        [:> Flex {:align "center" :gap "5"}
         [:> Switch {:checked @enable-database-schema
                     :size "3"
                     :onCheckedChange #(reset! enable-database-schema %)}]
         [:> Box
          [:> Text {:as "h4" :size "3" :weight "medium"} "Database schema"]
          [:> Text {:as "p" :size "2"} "Show database schema in the Editor section."]]]]]])

   [:> Grid {:columns "5" :gap "7"}
    [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
     [:> Text {:size "4" :weight "bold" :class "text-gray-12"} "Access modes"]
     [:> Text {:size "3" :class "text-gray-11"} "Setup how users interact with this connection."]]
    [:> Flex {:direction "column" :gap "7" :grid-column "span 3 / span 3"}
     [:> Box {:class "space-y-radix-5"}
      [:> Flex {:align "center" :gap "5"}
       [:> Switch {:checked @access-mode-runbooks
                   :size "3"
                   :disabled (access-mode-runbooks-disabled? @connection-type @connection-subtype)
                   :onCheckedChange #(reset! access-mode-runbooks %)}]
       [:> Box
        [:> Text {:as "h4" :size "3" :weight "medium"} "Runbooks"]
        [:> Text {:as "p" :size "2"} "Create templates to automate tasks in your organization. "]]]]
     [:> Box {:class "space-y-radix-5"}
      [:> Flex {:align "center" :gap "5"}
       [:> Switch {:checked @access-mode-connect
                   :size "3"
                   :disabled (access-mode-connect-disabled? @connection-type @connection-subtype)
                   :onCheckedChange #(reset! access-mode-connect %)}]
       [:> Box
        [:> Text {:as "h4" :size "3" :weight "medium"} "Native"]
        [:> Text {:as "p" :size "2"} (str "Access from your client of preference using hoop.dev to channel "
                                          "connections using our Desktop App or our Command Line Interface.")]]]]
     [:> Box {:class "space-y-radix-5"}
      [:> Flex {:align "center" :gap "5"}
       [:> Switch {:checked @access-mode-exec
                   :size "3"
                   :disabled (access-mode-exec-disabled? @connection-type @connection-subtype)
                   :onCheckedChange #(reset! access-mode-exec %)}]
       [:> Box
        [:> Text {:as "h4" :size "3" :weight "medium"} "Web and one-offs"]
        [:> Text {:as "p" :size "2"} (str "Use hoop.dev's developer portal or our "
                                          "CLI's One-Offs commands directly in your terminal.")]]]]]]])
