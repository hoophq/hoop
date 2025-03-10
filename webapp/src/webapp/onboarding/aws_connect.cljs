(ns webapp.onboarding.aws-connect
  (:require [re-frame.core :as rf]
            ["@radix-ui/themes" :refer [Badge Box Card Spinner Flex Heading Separator Text Callout]]
            [webapp.components.forms :as forms]
            ["lucide-react" :refer [Check Info ArrowUpRight]]
            [webapp.connections.views.setup.page-wrapper :as page-wrapper]
            [webapp.onboarding.setup-resource :refer [aws-resources-data-table]]
            [webapp.components.data-table-advance :refer [data-table-advanced]]
            [webapp.config :as config]))

(def steps
  [{:id :credentials
    :number 1
    :title "Credentials"}
   {:id :resources
    :number 2
    :title "Resources"}
   {:id :review
    :number 3
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

(defn loading-screen []
  (let [loading @(rf/subscribe [:aws-connect/loading])]
    (when (:active? loading)
      [:> Box {:class "absolute inset-0 bg-gray-1 flex flex-col items-center justify-center z-50"}
       [:> Box {:class "flex flex-col items-center justify-center p-8 rounded-lg"}

        ;; Loading spinner
        [:> Spinner {:class "mb-6"}]

        ;; Loading message
        [:> Text {:as "p" :size "2" :align "center" :class "text-[--gray-11]"}
         (:message loading)]]])))

(defn credentials-step []
  (let [credentials @(rf/subscribe [:aws-connect/credentials])
        error @(rf/subscribe [:aws-connect/error])]
    [:> Box {:class "space-y-7 max-w-[600px] relative"}
     [loading-screen]

     [:> Box
      [:> Box {:class "space-y-3"}
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        "IAM User Credentials"]
       [:> Text {:as "p" :size "2" :class "text-[--gray-11]" :mb "5"}
        "These keys provide secure programmatic access to your AWS environment and will be used only for discovering and managing your selected resources."]]]

     ;; Error message (if any)
     (when error
       [:> Card {:variant "surface" :color "red" :mb "4"}
        [:> Flex {:gap "2" :align "center"}
         [:> Text {:size "2" :color "red"} error]]])

     ;; IAM User Credentials
     [:> Box {:class "space-y-5"}
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

(defn resources-step []
  (let [errors @(rf/subscribe [:aws-connect/resources-errors])]
    [:> Flex {:direction "column" :align "center" :gap "7" :mb "4" :class "w-full"}
     [:> Box {:class "max-w-[600px] space-y-3"}
      [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
       "AWS Resources"]
      [:> Text {:as "p" :size "2" :class "text-[--gray-11]" :mb "5"}
       "Select the specific AWS resources you wish to connect. You may choose multiple resources across your accounts to enable full functionality."]

      [:> Callout.Root
       [:> Callout.Icon
        [:> Info {:size 16}]]
       [:> Callout.Text
        "During this Beta release, our service currently supports database resources only."]]]

     ;; Using our custom AWS resources data table component
     [:> Box {:class "w-full"}
      [aws-resources-data-table]]

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
        assignments @(rf/subscribe [:aws-connect/agent-assignments])
        agents @(rf/subscribe [:aws-connect/agents])]
    [:> Flex {:direction "column" :align "center" :gap "7" :mb "4" :class "w-full"}
     [:> Box {:class "max-w-[600px] space-y-3"}
      [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
       "Review Selected Resources"]
      [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
       "Please review your selected AWS database resources and assign an Agent to each connection before proceeding. Agents might be already suggested depending on the environment setup and work to facilitate secure communication between our service and your AWS resources."]

      [:> Flex {:align "center" :gap "1" :class "text-[--accent-a11] cursor-pointer"}
       [:> Text {:as "a"
                 :size "2"
                 :onClick #(rf/dispatch [:modal/show-agent-info])}
        "Learn more about Agents"]
       [:> ArrowUpRight {:size 16}]]]

     ;; Using data-table-advanced component
     [:> Box {:class "w-full"}
      [data-table-advanced
       {:columns [{:id :name
                   :header "Resource"
                   :accessor #(:name %)
                   :width "30%"}
                  {:id :connection_name
                   :header "Connection Name"
                   :accessor #(str (:name %) "-" (:account-id %))
                   :width "30%"}
                  {:id :agent
                   :header "Agent"
                   :width "40%"
                   :render (fn [_ row]
                             [forms/select
                              {:selected (get assignments (:id row) "")
                               :not-margin-bottom? true
                               :full-width? true
                               :on-change #(rf/dispatch [:aws-connect/set-agent-assignment (:id row) %])
                               :options (if (seq agents)
                                          ;; Usar agentes reais da API
                                          (map (fn [agent]
                                                 {:value (:id agent)
                                                  :text (:name agent)})
                                               agents)
                                          ;; Fallback para opções padrão se não houver agentes
                                          [])}])}]
        :data (filter #(contains? selected (:id %)) resources)
        :key-fn :id
        :sticky-header? true
        :empty-state "No resources selected yet"}]]]))

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
  (let [current-step @(rf/subscribe [:aws-connect/current-step])
        loading @(rf/subscribe [:aws-connect/loading])]
    [page-wrapper/main
     {:children
      [:> Box {:class "min-h-screen bg-gray-1"}
       [:> Box {:class "mx-auto max-w-[800px] p-6 space-y-7"}
        [:> Box {:class "place-items-center space-y-7"}
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
                            :completed? (> (.indexOf [:credentials :resources :review] current-step)
                                           (.indexOf [:credentials :resources :review] id))}]
              [step-title {:title title
                           :active? (= id current-step)
                           :completed? (> (.indexOf [:credentials :resources :review] current-step)
                                          (.indexOf [:credentials :resources :review] id))}]
              (when (> (.indexOf [:credentials :resources :review] current-step)
                       (.indexOf [:credentials :resources :review] id))
                [step-checkmark])]
             (when-not (= id :review)
               [:> Box {:class "px-2"}
                [:> Separator {:size "1" :orientation "horizontal" :class "w-4"}]])])]
       ;; Current step content
         (case current-step
           :credentials [credentials-step]
           :resources [resources-step]
           :review [review-step]
           [credentials-step])]]]
      :footer-props
      {:form-type :onboarding
       :back-text (case current-step
                    :credentials "Back"
                    :resources "Back to Credentials"
                    :review "Back to Resources")
       :next-text (case current-step
                    :credentials "Next: Resources"
                    :resources "Next: Review"
                    :review "Confirm and Create")
       :on-back #(case current-step
                   :credentials (rf/dispatch [:onboarding/back])
                   :resources (rf/dispatch [:aws-connect/set-current-step :credentials])
                   :review (rf/dispatch [:aws-connect/set-current-step :resources]))
       :on-next #(case current-step
                   :credentials (rf/dispatch [:aws-connect/validate-credentials])
                   :resources (rf/dispatch [:aws-connect/set-current-step :review])
                   :review (rf/dispatch [:aws-connect/create-connections]))
       :next-disabled? (case current-step
                         :credentials (:active? loading)
                         :resources (empty? @(rf/subscribe [:aws-connect/selected-resources]))
                         :review (some empty? (vals @(rf/subscribe [:aws-connect/agent-assignments]))))}}]))
