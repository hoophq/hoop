(ns webapp.resources.configure-role.terminal-access-tab
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout Flex Heading Link Switch
                               Text]]
   ["lucide-react" :refer [ArrowUpRight Star]]
   [re-frame.core :as rf]
   [webapp.components.multiselect :as multi-select]
   [webapp.config :as config]
   [webapp.connections.dlp-info-types :as dlp-info-types]
   [webapp.connections.helpers :as helpers]
   [webapp.routes :as routes]
   [webapp.upgrade-plan.main :as upgrade-plan]))

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

(defn main [connection form-type]
  (let [user-groups (rf/subscribe [:user-groups])
        user (rf/subscribe [:users->current-user])
        gateway-info (rf/subscribe [:gateway->info])
        guardrails-list (rf/subscribe [:guardrails->list])
        jira-templates-list (rf/subscribe [:jira-templates->list])
        access-modes (rf/subscribe [:connection-setup/access-modes])
        database-schema? (rf/subscribe [:connection-setup/database-schema])
        jira-template-id (rf/subscribe [:connection-setup/jira-template-id])
        guardrails (rf/subscribe [:connection-setup/guardrails])
        review? (rf/subscribe [:connection-setup/review])
        review-groups (rf/subscribe [:connection-setup/review-groups])
        data-masking? (rf/subscribe [:connection-setup/data-masking])
        data-masking-types (rf/subscribe [:connection-setup/data-masking-types])
        is-database? (= (:type connection) "database")]

    (rf/dispatch [:users->get-user-groups])

    (fn [_connection]
      (let [free-license? (-> @user :data :free-license?)
            has-redact-credentials? (-> @gateway-info :data :has_redact_credentials)
            redact-provider (-> @gateway-info :data :redact_provider)
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
           :description "Require approval prior to resource role execution."
           :checked @review?
           :on-change #(rf/dispatch [:connection-setup/toggle-review])
           :disabled? free-license?
           :complement-component (when @review?
                                   [:> Box {:mt "4"}
                                    [multi-select/main
                                     {:options (helpers/array->select-options @user-groups)
                                      :id "approval-groups-input"
                                      :name "approval-groups-input"
                                      :required? @review?
                                      :default-value @review-groups
                                      :on-change #(rf/dispatch [:connection-setup/set-review-groups (js->clj %)])}]])
           :upgrade-plan-component
           (when (or free-license?
                     (= form-type :onboarding))
             [:> Callout.Root {:size "2" :mt "4" :mb "4"}
              [:> Callout.Icon
               [:> Star {:size 16}]]
              [:> Callout.Text {:class "text-gray-12"}
               "Enable Command reviews by "
               [:> Link {:href "#"
                         :underline "always"
                         :class "text-primary-10"
                         :on-click (fn []
                                     (if (= form-type :onboarding)
                                       (rf/dispatch [:modal->open
                                                     {:content [:div {:class "bg-gray-1 min-h-full"}
                                                                [upgrade-plan/main true]]}])
                                       (rf/dispatch [:navigate :upgrade-plan])))}
                "upgrading your plan."]]])
           :learning-component
           [:> Link {:href (get-in config/docs-url [:features :jit-reviews])
                     :target "_blank"}
            [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
             [:> Callout.Icon
              [:> ArrowUpRight {:size 16}]]
             [:> Callout.Text
              "Learn more about Reviews"]]]}]

         ;; AI Data Masking
         [toggle-section
          {:title "AI Data Masking"
           :description "Provide an additional layer of security by ensuring sensitive data is masked in query results with AI-powered data masking."
           :checked @data-masking?
           :disabled? (or free-license?
                          (= form-type :onboarding)
                          (not has-redact-credentials?)
                          (= redact-provider "mspresidio"))
           :on-change #(rf/dispatch [:connection-setup/toggle-data-masking])
           :complement-component
           (when @data-masking?
             [:> Box {:mt "4"}
              [multi-select/main
               {:options (helpers/array->select-options
                          (case redact-provider
                            "mspresidio" dlp-info-types/presidio-options
                            "gcp" dlp-info-types/gcp-options
                            dlp-info-types/gcp-options))
                :id "data-masking-groups-input"
                :name "data-masking-groups-input"
                :required? @data-masking?
                :default-value @data-masking-types
                :disabled? (or free-license?
                               (= form-type :onboarding)
                               (not has-redact-credentials?))
                :on-change #(rf/dispatch [:connection-setup/set-data-masking-types (js->clj %)])}]])
           :upgrade-plan-component
           (when (or free-license?
                     (= form-type :onboarding))
             [:> Callout.Root {:size "2" :mt "4" :mb "4"}
              [:> Callout.Icon
               [:> Star {:size 16}]]
              [:> Callout.Text {:class "text-gray-12"}
               "Enable AI Data Masking by "
               [:> Link {:underline "always"
                         :href "#"
                         :class "text-primary-10"
                         :onClick #(rf/dispatch [:navigate :upgrade-plan])}
                "upgrading your plan."]]])
           :learning-component [:<>
                                [:> Link {:href (get-in config/docs-url [:features :ai-datamasking])
                                          :target "_blank"}
                                 [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
                                  [:> Callout.Icon
                                   [:> ArrowUpRight {:size 16}]]
                                  [:> Callout.Text
                                   "Learn more about AI Data Masking"]]]
                                (when (= redact-provider "mspresidio")
                                  [:> Link {:href (routes/url-for :ai-data-masking)
                                            :target "_blank"}
                                   [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
                                    [:> Callout.Icon
                                     [:> ArrowUpRight {:size 16}]]
                                    [:> Callout.Text
                                     "Go to AI Data Masking Management"]]])]}]

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
          [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-gray-12"}
           "Guardrails"]
          [:> Text {:as "p" :size "2" :class "text-gray-11"}
           "Create custom rules to guide and protect usage within your resource roles."]

          [:> Box {:class "mt-4"}
           [multi-select/main
            {:options (or (mapv #(into {} {"value" (:id %) "label" (:name %)})
                                (-> @guardrails-list :data)) [])
             :id "guardrails-input"
             :name "guardrails-input"
             :default-value (or @guardrails [])
             :on-change #(rf/dispatch [:connection-setup/set-guardrails (js->clj %)])}]]

          [:> Link {:href (routes/url-for :guardrails)
                    :on-click #(rf/dispatch [:modal->close])}
           [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
            [:> Callout.Icon
             [:> ArrowUpRight {:size 16}]]
            [:> Callout.Text
             "Go to Guardrails"]]]]

         ;; Jira Templates
         [:> Box
          [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-gray-12"}
           "Jira Templates"]
          [:> Text {:as "p" :size "2" :class "text-gray-11"}
           "Optimize and automate workflows with Jira Integration."]

          [:> Box {:class "mt-4"}
           [multi-select/single
            {:placeholder "Select one"
             :options (or (mapv #(into {} {"value" (:id %) "label" (:name %)})
                                (-> @jira-templates-list :data)) [])
             :id "jira-template-select"
             :name "jira-template-select"
             :default-value (when @jira-template-id [@jira-template-id])
             :clearable? true
             :on-change #(rf/dispatch [:connection-setup/set-jira-template-id (js->clj %)])}]]

          [:> Link {:href (routes/url-for :jira-templates)}
           [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
            [:> Callout.Icon
             [:> ArrowUpRight {:size 16}]]
            [:> Callout.Text
             "Go to JIRA Integration"]]]]

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

