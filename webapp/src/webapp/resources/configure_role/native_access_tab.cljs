(ns webapp.resources.configure-role.native-access-tab
  (:require
   ["@radix-ui/themes" :refer [Box Callout Flex Heading Link Switch Text]]
   ["lucide-react" :refer [ArrowUpRight Star]]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multi-select]
   [webapp.config :as config]
   [webapp.connections.dlp-info-types :as dlp-info-types]
   [webapp.connections.helpers :as helpers]
   [webapp.routes :as routes]))

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

(defn main [_connection]
  (let [user-groups (rf/subscribe [:user-groups])
        user (rf/subscribe [:users->current-user])
        gateway-info (rf/subscribe [:gateway->info])
        access-modes (rf/subscribe [:connection-setup/access-modes])
        review? (rf/subscribe [:connection-setup/review])
        review-groups (rf/subscribe [:connection-setup/review-groups])
        min-review-approvals (rf/subscribe [:connection-setup/min-review-approvals])
        force-approve-groups (rf/subscribe [:connection-setup/force-approve-groups])
        data-masking? (rf/subscribe [:connection-setup/data-masking])
        data-masking-types (rf/subscribe [:connection-setup/data-masking-types])]

    (rf/dispatch [:users->get-user-groups])

    (fn [_connection form-type]
      (let [free-license? (-> @user :data :free-license?)
            has-redact-credentials? (-> @gateway-info :data :has_redact_credentials)
            redact-provider (-> @gateway-info :data :redact_provider)
            native-access-enabled? (get @access-modes :native true)]

        [:> Box {:class "max-w-[600px] space-y-8"}

         ;; Native access availability
         [toggle-section
          {:title "Native access availability"
           :description "Access from your client of preference using hoop.dev to channel resource roles using our Desktop App or our Command Line Interface."
           :checked native-access-enabled?
           :on-change #(rf/dispatch [:connection-setup/toggle-access-mode :native])}]

         ;; Just-in-Time Review
         [toggle-section
          {:title "Just-in-Time Review"
           :description "Require approval prior to resource role execution."
           :checked @review?
           :on-change #(rf/dispatch [:connection-setup/toggle-review])
           :complement-component (when @review?
                                   [:> Box {:mt "4" :class "space-y-4"}

                                    [multi-select/main
                                     {:options (helpers/array->select-options @user-groups)
                                      :label "Approval user groups"
                                      :id "approval-groups-input"
                                      :name "approval-groups-input"
                                      :required? @review?
                                      :default-value @review-groups
                                      :on-change #(rf/dispatch [:connection-setup/set-review-groups (js->clj %)])}]

                                    [forms/input
                                     {:label "Minimum approval amount (optional)"
                                      :type "number"
                                      :id "min-review-approvals-input"
                                      :name "min-review-approvals-input"
                                      :value (if (some? @min-review-approvals) (str @min-review-approvals) "")
                                      :on-change #(let [val (-> % .-target .-value)]
                                                    (rf/dispatch [:connection-setup/set-min-review-approvals
                                                                  (when (not= val "")
                                                                    (js/parseInt val 10))]))
                                      :min 1}]

                                    [multi-select/main
                                     {:options (helpers/array->select-options @user-groups)
                                      :label "Force approval groups (optional)"
                                      :id "force-approve-groups-input"
                                      :name "force-approve-groups-input"
                                      :default-value @force-approve-groups
                                      :on-change #(rf/dispatch [:connection-setup/set-force-approve-groups (js->clj %)])}]])
           :learning-component
           [:> Link {:href (get-in config/docs-url [:features :jit-reviews])
                     :target "_blank"}
            [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
             [:> Callout.Icon
              [:> ArrowUpRight {:size 16}]]
             [:> Callout.Text
              "Learn more about Just-in-Time Reviews"]]]}]

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
                                     "Go to AI Data Masking Management"]]])]}]]))))

