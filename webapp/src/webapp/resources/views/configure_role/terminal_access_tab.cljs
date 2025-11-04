(ns webapp.resources.views.configure-role.terminal-access-tab
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout Flex Heading Link Switch Text]]
   ["lucide-react" :refer [ArrowUpRight Star]]
   [re-frame.core :as rf]
   [webapp.components.multiselect :as multi-select]))

(defn toggle-section
  [{:keys [title
           description
           checked
           disabled?
           on-change
           complement-component
           upgrade-plan-component
           learning-component]}]
  [:> Flex {:align "center" :gap "5"}
   [:> Switch {:checked checked
               :size "3"
               :disabled disabled?
               :onCheckedChange on-change}]
   [:> Box
    [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12]"} title]
    [:> Text {:as "p" :size "2" :class "text-[--gray-11]"} description]

    (when complement-component
      complement-component)

    (when upgrade-plan-component
      upgrade-plan-component)

    (when learning-component
      learning-component)]])

(defn main [connection]
  (let [user (rf/subscribe [:users->current-user])
        guardrails-list (rf/subscribe [:guardrails->list])
        jira-templates-list (rf/subscribe [:jira-templates->list])
        access-modes (rf/subscribe [:connection-setup/access-modes])
        database-schema? (rf/subscribe [:connection-setup/database-schema])
        jira-template-id (rf/subscribe [:connection-setup/jira-template-id])
        guardrails (rf/subscribe [:connection-setup/guardrails])
        is-database? (= (:type connection) "database")]

    (fn [_connection]
      (let [free-license? (-> @user :data :free-license?)
            web-terminal-enabled? (get @access-modes :web true)
            runbooks-enabled? (get @access-modes :runbooks true)]

        [:> Box {:class "max-w-[600px] space-y-8"}

         ;; Terminal access availability
         [toggle-section
          {:title "Terminal access availability"
           :description "Use hoop.dev's Web Terminal or our CLI's One-Offs commands directly in your terminal."
           :checked web-terminal-enabled?
           :on-change #(rf/dispatch [:connection-setup/toggle-access-mode :web])}]

         ;; Review by Command
         [toggle-section
          {:title "Review by Command"
           :description "Require approval prior to connection execution."
           :checked false
           :disabled? true
           :upgrade-plan-component
           (when free-license?
             [:> Callout.Root {:size "1" :class "mt-4" :color "blue"}
              [:> Callout.Icon [:> Star {:size 16}]]
              [:> Callout.Text
               "Enable Review by Command by "
               [:> Link {:onClick #(rf/dispatch [:navigate :upgrade-plan])}
                "upgrading your plan."]]])
           :learning-component
           [:> Button {:variant "ghost" :size "2" :class "mt-3"}
            [:> ArrowUpRight {:size 16}]
            "Learn more about Reviews"]}]

         ;; AI Data Masking
         [toggle-section
          {:title "AI Data Masking"
           :description "Provide an additional layer of security by ensuring sensitive data is masked in query results with AI-powered data masking."
           :checked false
           :disabled? true
           :upgrade-plan-component
           (when free-license?
             [:> Callout.Root {:size "1" :class "mt-4" :color "blue"}
              [:> Callout.Icon [:> Star {:size 16}]]
              [:> Callout.Text
               "Enable AI Data Masking by "
               [:> Link {:onClick #(rf/dispatch [:navigate :upgrade-plan])}
                "upgrading your plan."]]])
           :learning-component
           [:> Button {:variant "ghost" :size "2" :class "mt-3"}
            [:> ArrowUpRight {:size 16}]
            "Learn more about AI Data Masking"]}]

         ;; Runbooks
         [toggle-section
          {:title "Runbooks"
           :description "Automate tasks in your organization from a git server source."
           :checked runbooks-enabled?
           :on-change #(rf/dispatch [:connection-setup/toggle-access-mode :runbooks])
           :learning-component
           [:> Button {:variant "ghost" :size "2" :class "mt-3"}
            [:> ArrowUpRight {:size 16}]
            "Learn more about Runbooks"]}]

         ;; Guardrails
         [:> Box
          [toggle-section
           {:title "Guardrails"
            :description "Create custom rules to guide and protect usage within your connections."
            :checked (seq @guardrails)
            :disabled? true}]

          (when (seq (:data @guardrails-list))
            [:> Box {:class "mt-4"}
             [multi-select/main
              {:options (map (fn [g] {:value (:id g) :label (:name g)})
                             (:data @guardrails-list))
               :selected @guardrails
               :on-change #(rf/dispatch [:connection-setup/set-guardrails %])
               :placeholder "Select one"}]])

          [:> Button {:variant "ghost" :size "2" :class "mt-3"}
           [:> ArrowUpRight {:size 16}]
           "Go to Guardrails"]]

         ;; Jira Templates
         [:> Box
          [toggle-section
           {:title "Jira Templates"
            :description "Optimize and automate workflows with Jira Integration."
            :checked (some? @jira-template-id)
            :disabled? true}]

          (when (seq (:data @jira-templates-list))
            [:> Box {:class "mt-4"}
             [multi-select/main
              {:options (map (fn [t] {:value (:id t) :label (:name t)})
                             (:data @jira-templates-list))
               :selected (when @jira-template-id [@jira-template-id])
               :on-change #(rf/dispatch [:connection-setup/set-jira-template (first %)])
               :placeholder "Select one"
               :single? true}]])

          [:> Button {:variant "ghost" :size "2" :class "mt-3"}
           [:> ArrowUpRight {:size 16}]
           "Go to Jira Templates"]]

         ;; Additional configuration
         (when is-database?
           [:<>
            [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12] mt-8 mb-4"}
             "Additional configuration"]

            ;; Database schema (only for databases)
            [toggle-section
             {:title "Database schema"
              :description "Show database schema in the Editor section."
              :checked @database-schema?
              :on-change #(rf/dispatch [:connection-setup/toggle-database-schema])}]])]))))

