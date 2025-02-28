(ns webapp.onboarding.aws-connect
  (:require [re-frame.core :as rf]
            ["@radix-ui/themes" :refer [Badge Box Card Container Flex Heading Separator Text Checkbox]]
            [webapp.components.forms :as forms]
            ["lucide-react" :refer [Check]]
            [webapp.connections.views.setup.page-wrapper :as page-wrapper]
            [webapp.config :as config]))

(def steps
  [{:id :credentials
    :number 1
    :title "Credentials"}
   {:id :accounts
    :number 2
    :title "Accounts"}
   {:id :resources
    :number 3
    :title "Resources"}
   {:id :review
    :number 4
    :title "Review and Create"}])

(defn- step-number [{:keys [number active? completed?]}]
  [:> Badge
   {:size "1"
    :radius "full"
    :variant "soft"
    :color (if active?
             "blue"
             "gray")}
   [:> Text {:size "1" :weight "bold" :class (cond
                                               completed? "text-[--gray-a11]"
                                               active? "text-[--indigo-a11]"
                                               :else "text-[--gray-a11] opacity-50")}
    number]])

(defn- step-title [{:keys [title active? completed?]}]
  [:> Text
   {:size "1"
    :weight "bold"
    :class (cond
             completed? "text-[--gray-a11]"
             active? "text-[--indigo-a11]"
             :else "text-[--gray-a11] opacity-50")}
   title])

(defn- step-checkmark []
  [:> Check
   {:size 16
    :class "text-[--gray-a11]"}])

(defn credentials-step []
  (let [credentials @(rf/subscribe [:aws-connect/credentials])]
    [:> Box {:class "space-y-7"}
     [:> Box
      [:> Box
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "AWS Authentication"]
       [:> Text {:as "p" :size "2" :class "text-[--gray-11]" :mb "5"}
        "Please provide either an IAM Role or IAM User Credentials below. Only one authentication method is required to proceed."]]

     ;; IAM Role input
      [forms/input
       {:placeholder "e.g. arn:aws:iam::<ACCOUNT_ID>:role/<ROLE_NAME>"
        :label "IAM Role"
        :value (get-in credentials [:iam-role])
        :on-change #(rf/dispatch [:aws-connect/set-iam-role (-> % .-target .-value)])}]]

     ;; IAM User Credentials
     [:> Box {:class "space-y-5"}
      [:> Box
       [:> Heading {:as "h3" :size "3" :weight "bold" :class "text-[--gray-12]"}
        "IAM User Credentials"]
       [:> Text {:as "p" :size "2" :class "text-[--gray-11]" :mb "4"}
        "These keys provide secure programmatic access to your AWS environment and will be used only for discovering and managing your selected resources."]]

      [forms/input
       {:placeholder "e.g. AKIAIOSFODNN7EXAMPLE"
        :label "Access Key ID"
        :value (get-in credentials [:iam-user :access-key-id])
        :on-change #(rf/dispatch [:aws-connect/set-iam-user-credentials :access-key-id (-> % .-target .-value)])}]

      [forms/input
       {:type "password"
        :placeholder "e.g. wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
        :label "Secret Access Key"
        :value (get-in credentials [:iam-user :secret-access-key])
        :on-change #(rf/dispatch [:aws-connect/set-iam-user-credentials :secret-access-key (-> % .-target .-value)])}]

      [forms/select
       {:label "Region"
        :full-width? true
        :selected (or (get-in credentials [:iam-user :region]) "")
        :on-change #(rf/dispatch [:aws-connect/set-iam-user-credentials :region %])
        :options [{:value "us-east-1" :text "US East (N. Virginia)"}
                  {:value "us-west-2" :text "US West (Oregon)"}
                  {:value "eu-west-1" :text "EU (Ireland)"}
                  {:value "ap-southeast-1" :text "Asia Pacific (Singapore)"}]}]

      [forms/textarea
       {:label "Session Token (Optional)"
        :placeholder "e.g. FOoGZXlvYXdzE0z/EaDFQNA2EY59z3tKrAdJB"
        :value (get-in credentials [:iam-user :session-token])
        :on-change #(rf/dispatch [:aws-connect/set-iam-user-credentials :session-token (-> % .-target .-value)])}]]]))

(defn accounts-step []
  (let [accounts @(rf/subscribe [:aws-connect/accounts])
        selected @(rf/subscribe [:aws-connect/selected-accounts])]
    [:> Box {:mb "4"}
     [:> Heading {:size "4"} "AWS Accounts"]
     [:> Text {:as "p" :size "2"}
      "Select the AWS account(s) you wish to integrate with. Your selection will determine which resources become available for later management."]

     [:> Card {:variant "surface"}
      [:> Box {:as "table" :width "100%"}
       [:thead
        [:tr
         [:th ""]
         [:th {:align "left" :p "2"} "Account ID"]
         [:th {:align "left" :p "2"} "Alias"]
         [:th {:align "left" :p "2"} "Status"]]]
       [:tbody
        (for [account accounts]
          ^{:key (:id account)}
          [:tr
           [:td {:p "2"}
            [:> Checkbox
             {:checked (contains? selected (:id account))
              :onCheckedChange #(rf/dispatch [:aws-connect/set-selected-accounts
                                              (if %
                                                (conj selected (:id account))
                                                (disj selected (:id account)))])}]]
           [:td {:p "2"} (:id account)]
           [:td {:p "2"} (:alias account)]
           [:td {:p "2"} (:status account)]])]]]]))

(defn resources-step []
  (let [resources @(rf/subscribe [:aws-connect/resources])
        selected @(rf/subscribe [:aws-connect/selected-resources])
        errors @(rf/subscribe [:aws-connect/resources-errors])]
    [:> Box {:mb "4"}
     [:> Heading {:size "4"} "AWS Resources"]
     [:> Text {:as "p" :size "2"}
      "Select the specific AWS resources you wish to connect. You may choose multiple resources across your accounts to enable full functionality."]

     [:> Card {:variant "surface" :color "blue"}
      [:> Flex {:gap "2" :align "center"}
       [:> Text {:size "2" :color "blue"}
        "During this Beta release, our service currently supports database resources only."]]]

     [:> Card {:variant "surface"}
      [:> Box {:as "table" :width "100%"}
       [:thead
        [:tr
         [:th ""]
         [:th {:align "left" :p "2"} "Resources"]
         [:th {:align "left" :p "2"} "Subnet CIDR"]
         [:th {:align "left" :p "2"} "VPC ID"]
         [:th {:align "left" :p "2"} "Status"]
         [:th {:align "left" :p "2"} "Security Group"]]]
       [:tbody
        (for [resource resources]
          ^{:key (:id resource)}
          [:tr
           [:td {:p "2"}
            [:> Checkbox
             {:checked (contains? selected (:id resource))
              :onCheckedChange #(rf/dispatch [:aws-connect/set-selected-resources
                                              (if %
                                                (conj selected (:id resource))
                                                (disj selected (:id resource)))])
              :disabled (contains? errors (:id resource))}]]
           [:td {:p "2"}
            [:> Flex {:gap "2" :align "center"}
             (when (contains? errors (:id resource))
               [:> Text {:color "red" :size "2"} "⚠"])
             (:name resource)]]
           [:td {:p "2"} (:subnet-cidr resource)]
           [:td {:p "2"} (:vpc-id resource)]
           [:td {:p "2"} (:status resource)]
           [:td {:p "2"}
            [:> Checkbox
             {:checked (:security-group-enabled? resource)
              :disabled true}]]])]]]

     (when (seq errors)
       [:> Card {:variant "surface" :color "red" :mt "4"}
        [:> Flex {:gap "2" :align "center"}
         [:> Text {:size "2" :color "red"}
          (str "There was one or more access errors for certain AWS resources. "
               "Please deselect these resources or verify the error details. All selected resources must be properly "
               "connected before proceeding to create connections.")]]])]))

(defn review-step []
  (let [resources @(rf/subscribe [:aws-connect/resources])
        selected @(rf/subscribe [:aws-connect/selected-resources])
        assignments @(rf/subscribe [:aws-connect/agent-assignments])]
    [:> Box {:mb "4"}
     [:> Heading {:size "4"} "Review Selected Resources"]
     [:> Text {:as "p" :size "2"}
      "Please review your selected AWS database resources and assign an Agent to each connection before proceeding. Agents might be already suggested depending on the environment setup and work to facilitate secure communication between our service and your AWS resources."]

     [:> Flex {:align "center" :gap "2" :mb "4"}
      [:> Text {:as "a"
                :size "2"
                :color "blue"
                :style {:cursor "pointer"}
                :onClick #(rf/dispatch [:modal/show-agent-info])}
       "Learn more about Agents"
       [:span "→"]]]

     [:> Card {:variant "surface"}
      [:> Box {:as "table" :width "100%"}
       [:thead
        [:tr
         [:th {:align "left" :p "2"} "Resource"]
         [:th {:align "left" :p "2"} "Agent"]]]
       [:tbody
        (for [resource-id selected
              :let [resource (first (filter #(= (:id %) resource-id) resources))]]
          ^{:key resource-id}
          [:tr
           [:td {:p "2"} (:name resource)]
           [:td {:p "2"}
            [forms/select
             {:selected (get assignments resource-id)
              :on-change #(rf/dispatch [:aws-connect/set-agent-assignment resource-id %])
              :options [{:value "default" :text "Default Agent"}
                        {:value "hoop-prd" :text "Hoop Production Agent"}]}]]])]]]]))

(defn aws-connect-header []
  [:<>
   [:> Box
    [:img {:src (str config/webapp-url "/images/hoop-branding/PNG/hoop-symbol_black@4x.png")
           :class "w-16 mx-auto py-4"}]]
   [:> Box
    [:> Heading {:as "h1" :align "center" :size "6" :mb "2" :weight "bold" :class "text-[--gray-12]"}
     "AWS Connect"]
    [:> Text {:as "p" :size "3" :align "center" :class "text-[--gray-12]"}
     "Follow the steps to setup your AWS resources."]]])

(defn main []
  (let [current-step @(rf/subscribe [:aws-connect/current-step])]
    [page-wrapper/main
     {:children
      [:> Box {:class "min-h-screen bg-gray-1"}
       [:> Box {:class "max-w-[610px] mx-auto p-6 space-y-7"}
        [:> Box {:class "space-y-7"}
         ;; Header
         [aws-connect-header]
         ;; Progress steps
         [:> Flex {:align "center" :justify "center" :mb "8" :class "w-full"}
          (for [{:keys [id number title]} steps]
            ^{:key id}
            [:> Flex {:align "center"}
             [:> Flex {:align "center" :gap "1"}
              [step-number {:number number
                            :active? (= id current-step)
                            :completed? (> (.indexOf [:credentials :accounts :resources :review] current-step)
                                           (.indexOf [:credentials :accounts :resources :review] id))}]
              [step-title {:title title
                           :active? (= id current-step)
                           :completed? (> (.indexOf [:credentials :accounts :resources :review] current-step)
                                          (.indexOf [:credentials :accounts :resources :review] id))}]
              (when (> (.indexOf [:credentials :accounts :resources :review] current-step)
                       (.indexOf [:credentials :accounts :resources :review] id))
                [step-checkmark])]
             (when-not (= id :review)
               [:> Box {:class "px-2"}
                [:> Separator {:size "1" :orientation "horizontal" :class "w-4"}]])])]
         ;; Current step content
         (case current-step
           :credentials [credentials-step]
           :accounts [accounts-step]
           :resources [resources-step]
           :review [review-step]
           [credentials-step])]]]
      :footer-props
      {:form-type :onboarding
       :back-text (case current-step
                    :credentials "Back"
                    :accounts "Back to Credentials"
                    :resources "Back to Accounts"
                    :review "Back to Resources")
       :next-text (case current-step
                    :credentials "Next: Accounts"
                    :accounts "Next: Resources"
                    :resources "Next: Review"
                    :review "Confirm and Create")
       :on-back #(case current-step
                   :credentials (rf/dispatch [:onboarding/back])
                   :accounts (rf/dispatch [:aws-connect/set-current-step :credentials])
                   :resources (rf/dispatch [:aws-connect/set-current-step :accounts])
                   :review (rf/dispatch [:aws-connect/set-current-step :resources]))
       :on-next #(case current-step
                   :credentials (rf/dispatch [:aws-connect/validate-credentials])
                   :accounts (rf/dispatch [:aws-connect/set-current-step :resources])
                   :resources (rf/dispatch [:aws-connect/set-current-step :review])
                   :review (rf/dispatch [:aws-connect/create-connections]))
       :next-disabled? (case current-step
                         :credentials (= @(rf/subscribe [:aws-connect/status]) :validating)
                         :accounts (empty? @(rf/subscribe [:aws-connect/selected-accounts]))
                         :resources (empty? @(rf/subscribe [:aws-connect/selected-resources]))
                         :review (some empty? (vals @(rf/subscribe [:aws-connect/agent-assignments]))))}}]))
