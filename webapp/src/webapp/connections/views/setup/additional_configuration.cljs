(ns webapp.connections.views.setup.additional-configuration
  (:require
   ["@radix-ui/themes" :refer [Box Callout Flex Heading Link Switch Text]]
   ["lucide-react" :refer [ArrowUpRight Star]]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multi-select]
   [webapp.config :as config]
   [webapp.connections.dlp-info-types :as dlp-info-types]
   [webapp.connections.helpers :as helpers]
   [webapp.connections.views.setup.tags-inputs :as tags-inputs]
   [webapp.routes :as routes]
   [webapp.upgrade-plan.main :as upgrade-plan]))

(defn- get-access-mode-defaults [selected-type]
  (if (= selected-type "tcp")
    {:runbooks false
     :native true
     :web false}
    {:runbooks true
     :native true
     :web true}))

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

(defn main [{:keys [show-database-schema? selected-type submit-fn form-type]}]
  (let [user-groups (rf/subscribe [:user-groups])
        user (rf/subscribe [:users->current-user])
        gateway-info (rf/subscribe [:gateway->info])
        guardrails-list (rf/subscribe [:guardrails->list])
        jira-templates-list (rf/subscribe [:jira-templates->list])
        review? (rf/subscribe [:connection-setup/review])
        review-groups (rf/subscribe [:connection-setup/review-groups])
        data-masking? (rf/subscribe [:connection-setup/data-masking])
        data-masking-types (rf/subscribe [:connection-setup/data-masking-types])
        database-schema? (rf/subscribe [:connection-setup/database-schema])
        access-modes (rf/subscribe [:connection-setup/access-modes])
        connection-name (rf/subscribe [:connection-setup/name])
        connection-subtype (rf/subscribe [:connection-setup/connection-subtype])
        jira-template-id (rf/subscribe [:connection-setup/jira-template-id])
        guardrails (rf/subscribe [:connection-setup/guardrails])
        tags (rf/subscribe [:connection-setup/tags])
        tags-input (rf/subscribe [:connection-setup/tags-input])
        is-tcp? (= @connection-subtype "tcp")
        default-modes (get-access-mode-defaults @connection-subtype)]

    (rf/dispatch [:users->get-user-groups])
    (rf/dispatch [:guardrails->get-all])
    (rf/dispatch [:jira-templates->get-all])

    (when is-tcp?
      (doseq [[mode value] default-modes]
        (when (not= value (get @access-modes mode))
          (rf/dispatch [:connection-setup/toggle-access-mode mode]))))
    (fn []
      (let [free-license? (-> @user :data :free-license?)
            has-redact-credentials? (-> @gateway-info :data :has_redact_credentials)
            redact-provider (-> @gateway-info :data :redact_provider)]
        [:form
         {:id "additional-config-form"
          :class "max-w-[600px]"
          :on-submit (fn [e]
                       (.preventDefault e)
                       (submit-fn))}
         [:> Box {:class "space-y-7"}
          (when-not (= form-type :update)
            [:> Box
             [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]" :mb "5"}
              "Connection information"]

             [forms/input {:placeholder (str (when selected-type
                                               (str selected-type "-"))
                                             (helpers/random-connection-name))
                           :label "Name"
                           :required true
                           :disabled (= form-type :update)
                           :value @connection-name
                           :on-change #(rf/dispatch [:connection-setup/set-name
                                                     (-> % .-target .-value)])}]])

          (when (not= @connection-subtype "console")
            [tags-inputs/main])

          [:> Box
           [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]" :mb "5"}
            "Additional Configuration"]
           [:> Box {:class "space-y-7"}
                                                                 ;; Reviews
            [:> Box {:class "space-y-2"}
             [toggle-section
              {:title "Reviews"
               :description "Require approval prior to connection execution. Enable Just-in-Time access for 30-minute sessions or Command reviews for individual query approvals."
               :checked @review?
               :on-change #(rf/dispatch [:connection-setup/toggle-review])
               :complement-component
               (when @review?
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
                             :class "text-primary-10"
                             :on-click (fn []
                                         (if (= form-type :onboarding)
                                           (rf/dispatch [:modal->open
                                                         {:content [:div {:class "bg-gray-1 min-h-full"}
                                                                    [upgrade-plan/main true]]}])
                                           (rf/dispatch [:navigate :upgrade-plan])))}
                    "upgrading your plan."]]])

               :learning-component [:> Link {:href (get-in config/docs-url [:features :jit-reviews])
                                             :target "_blank"}
                                    [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
                                     [:> Callout.Icon
                                      [:> ArrowUpRight {:size 16}]]
                                     [:> Callout.Text
                                      "Learn more about Reviews"]]]}]]

          ;; AI Data Masking
            [:> Box {:class "space-y-2"}
             [toggle-section
              {:title "AI Data Masking"
               :description "Provide an additional layer of security by ensuring sensitive data is masked in query results with AI-powered data masking."
               :checked @data-masking?
               :disabled? (or free-license?
                              (= form-type :onboarding)
                              (not has-redact-credentials?))
               :on-change #(rf/dispatch [:connection-setup/toggle-data-masking])

               :complement-component
               (when @data-masking?
                 [:> Box {:mt "4"}
                  [multi-select/main
                   {:options (helpers/array->select-options
                              (case redact-provider
                                "presidio" dlp-info-types/presidio-options
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
                   [:> Link {:href "#"
                             :class "text-primary-10"
                             :on-click (fn []
                                         (if (= form-type :onboarding)
                                           (rf/dispatch [:modal->open
                                                         {:content [:div {:class "bg-gray-1 min-h-full"}
                                                                    [upgrade-plan/main true]]}])
                                           (rf/dispatch [:navigate :upgrade-plan])))}
                    "upgrading your plan."]]])

               :learning-component [:> Link {:href (get-in config/docs-url [:features :ai-datamasking])
                                             :target "_blank"}
                                    [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
                                     [:> Callout.Icon
                                      [:> ArrowUpRight {:size 16}]]
                                     [:> Callout.Text
                                      "Learn more about AI Data Masking"]]]}]]

           ;; Database schema
            (when show-database-schema?
              [:> Box {:class "space-y-2"}
               [toggle-section
                {:title "Database schema"
                 :description "Show database schema in the Editor section."
                 :checked @database-schema?
                 :on-change #(rf/dispatch [:connection-setup/toggle-database-schema])}]])]]

          (when (not= @connection-subtype "console")
            [:> Box
             [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
              "Access modes"]
             [:> Text {:as "p" :size "3" :class "text-[--gray-11]" :mb "5"}
              "Setup how users interact with this connection."]

             [:> Box {:class "space-y-7"}
              [toggle-section
               {:title "Runbooks"
                :description "Create templates to automate tasks in your organization."
                :disabled? is-tcp?
                :checked (if is-tcp?
                           (:runbooks default-modes)
                           (get @access-modes :runbooks true))
                :on-change #(rf/dispatch [:connection-setup/toggle-access-mode :runbooks])}]

              [toggle-section
               {:title "Native"
                :description "Access from your client of preference using hoop.dev to channel connections using our Desktop App or our Command Line Interface."
                :disabled? is-tcp?
                :checked (if is-tcp?
                           (:native default-modes)
                           (get @access-modes :native true))
                :on-change #(rf/dispatch [:connection-setup/toggle-access-mode :native])}]

              [toggle-section
               {:title "Web and one-offs"
                :description "Use hoop.dev's developer portal or our CLI's One-Offs commands directly in your terminal."
                :disabled? is-tcp?
                :checked (if is-tcp?
                           (:web default-modes)
                           (get @access-modes :web true))
                :on-change #(rf/dispatch [:connection-setup/toggle-access-mode :web])}]]])

          (when-not (or (= form-type :onboarding)
                        (= @connection-subtype "console")
                        (= @connection-subtype "tcp"))
            [:<>
             [:> Box
              [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
               "Guardrails"]
              [:> Text {:as "p" :size "3" :class "text-[--gray-11]" :mb "5"}
               "Create custom rules to guide and protect usage within your connections."]

              [multi-select/main {:options (or (mapv #(into {} {"value" (:id %) "label" (:name %)})
                                                     (-> @guardrails-list :data)) [])
                                  :id "guardrails-input"
                                  :name "guardrails-input"
                                  :default-value (or @guardrails [])
                                  :on-change #(rf/dispatch [:connection-setup/set-guardrails (js->clj %)])}]

              [:> Link {:href (routes/url-for :guardrails)
                        :on-click #(rf/dispatch [:modal->close])}
               [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
                [:> Callout.Icon
                 [:> ArrowUpRight {:size 16}]]
                [:> Callout.Text
                 "Go to Guardrails"]]]]

             [:> Box
              [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
               "Jira Templates"]
              [:> Text {:as "p" :size "3" :class "text-[--gray-11]" :mb "5"}
               "Optimize and automate workflows with Jira Integration."]

              [multi-select/single {:placeholder "Select one"
                                    :options (or (mapv #(into {} {"value" (:id %) "label" (:name %)})
                                                       (-> @jira-templates-list :data)) [])
                                    :id "jira-template-select"
                                    :name "jira-template-select"
                                    :default-value (or @jira-template-id nil)
                                    :clearable? true
                                    :on-change #(rf/dispatch [:connection-setup/set-jira-template-id (js->clj %)])}]

              [:> Link {:href (routes/url-for :jira-templates)}
               [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
                [:> Callout.Icon
                 [:> ArrowUpRight {:size 16}]]
                [:> Callout.Text
                 "Go to JIRA Integration"]]]]])]]))))
