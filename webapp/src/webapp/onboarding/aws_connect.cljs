(ns webapp.onboarding.aws-connect
  (:require [re-frame.core :as rf]
            ["@radix-ui/themes" :refer [Badge Box Button Card Spinner Link Flex Heading Separator Text Callout Switch]]
            [webapp.components.forms :as forms]
            ["lucide-react" :refer [Check Info ArrowUpRight X]]
            [webapp.connections.views.setup.page-wrapper :as page-wrapper]
            [webapp.onboarding.setup-resource :refer [aws-resources-data-table]]
            [webapp.components.data-table-simple :refer [data-table-simple]]
            [webapp.config :as config]
            [reagent.core :as r]))

(def steps
  [{:id :credentials
    :number 1
    :title "Credentials"}
   {:id :resources
    :number 2
    :title "Resources"}
   {:id :review
    :number 3
    :title "Review and Create"}
   {:id :creation-status
    :number 4
    :title "Status"}])

(def aws-regions
  [{:value "us-east-1" :text "us-east-1"}
   {:value "us-east-2" :text "us-east-2"}
   {:value "us-west-1" :text "us-west-1"}
   {:value "us-west-2" :text "us-west-2"}
   {:value "af-south-1" :text "af-south-1"}
   {:value "ap-east-1" :text "ap-east-1"}
   {:value "ap-south-1" :text "ap-south-1"}
   {:value "ap-northeast-1" :text "ap-northeast-1"}
   {:value "ap-northeast-2" :text "ap-northeast-2"}
   {:value "ap-northeast-3" :text "ap-northeast-3"}
   {:value "ap-southeast-1" :text "ap-southeast-1"}
   {:value "ap-southeast-2" :text "ap-southeast-2"}
   {:value "ap-southeast-3" :text "ap-southeast-3"}
   {:value "ap-southeast-4" :text "ap-southeast-4"}
   {:value "ca-central-1" :text "ca-central-1"}
   {:value "ca-west-1" :text "ca-west-1"}
   {:value "eu-central-1" :text "eu-central-1"}
   {:value "eu-central-2" :text "eu-central-2"}
   {:value "eu-west-1" :text "eu-west-1"}
   {:value "eu-west-2" :text "eu-west-2"}
   {:value "eu-west-3" :text "eu-west-3"}
   {:value "eu-south-1" :text "eu-south-1"}
   {:value "eu-south-2" :text "eu-south-2"}
   {:value "eu-north-1" :text "eu-north-1"}
   {:value "il-central-1" :text "il-central-1"}
   {:value "me-central-1" :text "me-central-1"}
   {:value "me-south-1" :text "me-south-1"}
   {:value "sa-east-1" :text "sa-east-1"}
   {:value "us-gov-east-1" :text "us-gov-east-1"}
   {:value "us-gov-west-1" :text "us-gov-west-1"}])

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
        :options aws-regions}]

      [forms/textarea
       {:label "Session Token (Optional)"
        :placeholder "e.g. FOoGZXlvYXdzE0z/EaDFQNA2EY59z3tKrAdJB"
        :value (get-in credentials [:iam-user :session-token])
        :on-change #(rf/dispatch [:aws-connect/set-iam-user-credentials :session-token (-> % .-target .-value)])}]]

          ;; Error message (if any)
     (when error
       [:> Card {:variant "surface" :color "red" :mb "4"}
        [:> Flex {:gap "2" :align "center"}
         [:> Text {:size "2" :color "red"} error]]])]))

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

     [:> Box {:class "w-full"}
      [aws-resources-data-table]]

     (when (seq errors)
       [:> Card {:variant "surface" :color "red" :mt "4"}
        [:> Flex {:gap "2" :align "center"}
         [:> Text {:size "2" :color "red"}
          (str "There was one or more access errors for certain AWS resources. "
               "Please deselect these resources or verify the error details. All selected resources must be properly "
               "connected before proceeding to create connections.")]]])]))

(defn create-connection-config []
  (let [create-connection @(rf/subscribe [:aws-connect/create-connection])]
    [:> Box {:class "w-full max-w-[600px] space-y-3"}
     [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
      "Additional Configuration"]

     [:> Flex {:align "center" :gap "4" :class "text-[--accent-a11] cursor-pointer"}
      [:> Switch {:checked create-connection
                  :on-checked-change #(rf/dispatch [:aws-connect/toggle-create-connection %])}]
      [:> Box
       [:> Text {:as "p" :size "3" :weight "medium" :class "text-[--gray-12]"}
        "Create Connection"]
       [:> Text {:as "p" :size "2" :class "text-[--gray-12]"}
        "When enabled, connections will be automatically created after configuring the resources."]]]]))

(defn review-step []
  (let [resources @(rf/subscribe [:aws-connect/resources])
        selected @(rf/subscribe [:aws-connect/selected-resources])
        assignments @(rf/subscribe [:aws-connect/agent-assignments])
        connection-names @(rf/subscribe [:aws-connect/connection-names])
        agents @(rf/subscribe [:aws-connect/agents])

        selected-resources (reduce (fn [acc account]
                                     (let [children (:children account)
                                           selected-children (filter #(contains? selected (:id %)) children)]
                                       (if (seq selected-children)
                                         (conj acc (assoc account :children selected-children))
                                         acc)))
                                   []
                                   resources)

        apply-agent-to-account (fn [account-id agent-id]
                                 (let [account (first (filter #(= (:id %) account-id) selected-resources))
                                       child-resources (:children account)]
                                   (doseq [resource child-resources]
                                     (rf/dispatch [:aws-connect/set-agent-assignment (:id resource) agent-id]))))]

    (when (and (seq resources) (empty? connection-names))
      (doseq [resource-id selected
              :let [resource-data
                    (some (fn [account]
                            (some #(when (= (:id %) resource-id) %) (:children account)))
                          resources)]
              :when resource-data]
        (let [default-name (str (:name resource-data) "-" (:account-id resource-data))]
          (rf/dispatch [:aws-connect/set-connection-name resource-id default-name]))))

    [:> Flex {:direction "column" :align "center" :gap "7" :mb "4" :class "w-full"}
     [:> Box {:class "max-w-[600px] space-y-3"}
      [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
       "Review Selected Resources"]
      [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
       "Please review your selected AWS database resources and assign an Agent to each connection before proceeding. You can also customize the connection names."]

      [:> Flex {:align "center" :gap "1" :class "text-[--accent-a11] cursor-pointer"}
       [:> Link {:href "https://hoop.dev/docs/concepts/agent"
                 :target "_blank"}
        [:> Flex {:gap "2" :align "center"}
         [:> Text {:as "a"
                   :size "2"}
          "Learn more about Agents"]
         [:> ArrowUpRight {:size 16}]]]]]

     [create-connection-config]

     [:> Box {:class "w-full"}
      [data-table-simple
       {:columns [{:id :name
                   :header "Resources"
                   :width "25%"}
                  {:id :id
                   :header "Account ID"
                   :width "15%"
                   :render (fn [_ row]
                             (if (:account-type row)
                               (:id row)
                               ""))}
                  {:id :connection_name
                   :header "Connection Name"
                   :width "30%"
                   :render (fn [_ row]
                             (if (:account-type row)
                               ""
                               (let [resource-id (:id row)
                                     default-name (str (:name row) "-" (:account-id row))
                                     current-name (get connection-names resource-id default-name)]
                                 [forms/input
                                  {:value current-name
                                   :not-margin-bottom? true
                                   :placeholder "Enter connection name"
                                   :on-change #(rf/dispatch [:aws-connect/set-connection-name
                                                             resource-id
                                                             (-> % .-target .-value)])}])))}
                  {:id :agent
                   :header "Agent"
                   :width "30%"
                   :render (fn [_ row]
                             (if (:account-type row)
                               ""

                               (let [resource-id (:id row)
                                     account-id (:account-id row)
                                     current-agent-id (get assignments resource-id "")
                                     agent-id (r/atom current-agent-id)]
                                 [:> Flex {:align "center" :gap "2"}
                                  [forms/select
                                   {:selected @agent-id
                                    :not-margin-bottom? true
                                    :style {:width "120px"}
                                    :on-change #(do (reset! agent-id %)
                                                    (rf/dispatch [:aws-connect/set-agent-assignment resource-id %]))
                                    :options (if (seq agents)
                                               (map (fn [agent]
                                                      {:value (:id agent)
                                                       :text (:name agent)})
                                                    agents)
                                               [])}]
                                  [:> Button {:size "1"
                                              :variant "soft"
                                              :disabled (empty? @agent-id)
                                              :on-click #(apply-agent-to-account account-id @agent-id)}
                                   "Apply to all"]])))}]
        :data selected-resources
        :key-fn :id
        :sticky-header? true
        :empty-state "No resources selected yet"}]]]))

(defn creation-status-step []
  (let [creation-status @(rf/subscribe [:aws-connect/creation-status])
        connections (:connections creation-status)
        connections-data (for [[id conn] (seq connections)]
                           (let [conn-data (assoc (:resource conn)
                                                  :id id
                                                  :connection-name (:name conn)
                                                  :connection-status (:status conn)
                                                  :connection-error (:error conn))]
                               ;; Formatação de erros para uso com data-table-simple
                             (if (:connection-error conn-data)
                               (assoc conn-data :error {:message (:connection-error conn-data)
                                                        :code "Error"
                                                        :type "Failed"})
                               conn-data)))
        sorted-connections (vec
                            (sort-by (fn [conn]
                                       (case (:connection-status conn)
                                         "pending" 0
                                         "failure" 1
                                         "success" 2
                                         3))
                                     connections-data))]
    [:> Flex {:direction "column" :align "center" :gap "7" :mb "4" :class "w-full"}
     [:> Box {:class "max-w-[600px] space-y-3"}
      [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
       "Creating Connections"]
      [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
       "Please wait while we create your AWS database connections. This process may take a few minutes."]]

     [:> Box {:class "w-full"}
      [data-table-simple
       {:columns [{:id :connection-name
                   :header "Connection Name"
                   :width "40%"}
                  {:id :engine
                   :header "Type"
                   :width "20%"}
                  {:id :status
                   :header "Status"
                   :width "20%"
                   :render (fn [_ row]
                             [:> Flex {:align "center" :gap "2"}
                              [:> Box {:class "w-6"}
                               (case (:connection-status row)
                                 "pending" [:> Spinner {:size "1"}]
                                 "success" [:> Check {:size 18 :class "text-green-500"}]
                                 "failure" [:> X {:size 18 :class "text-red-500"}]
                                 [:> Box])]
                              [:> Badge {:color (case (:connection-status row)
                                                  "pending" "blue"
                                                  "success" "green"
                                                  "failure" "red"
                                                  "gray")
                                         :variant "soft"}
                               (case (:connection-status row)
                                 "pending" "Creating..."
                                 "success" "Created"
                                 "failure" "Failed"
                                 (:status row))]])}]
        :data sorted-connections
        :key-fn :id
        :sticky-header? true
        :empty-state "No connections are being created"}]]]))

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
  (fn [form-type]
    (let [current-step @(rf/subscribe [:aws-connect/current-step])
          loading @(rf/subscribe [:aws-connect/loading])]

      [page-wrapper/main
       {:children
        [:> Box {:class "min-h-screen bg-gray-1"}
         [:> Box {:class "mx-auto max-w-[1000px] p-6 space-y-7"}
          [:> Box {:class "place-items-center space-y-7"}
           [aws-connect-header]
           [:> Flex {:align "center" :justify "center" :mb "8" :class "w-full"}
            (for [{:keys [id number title]} (if (= current-step :creation-status)
                                              steps
                                              (take 3 steps))]
              ^{:key id}
              [:> Flex {:align "center"}
               [:> Flex {:align "center" :gap "1"}
                [step-number {:number number
                              :active? (= id current-step)
                              :completed? (> (.indexOf [:credentials :resources :review :creation-status] current-step)
                                             (.indexOf [:credentials :resources :review :creation-status] id))}]
                [step-title {:title title
                             :active? (= id current-step)
                             :completed? (> (.indexOf [:credentials :resources :review :creation-status] current-step)
                                            (.indexOf [:credentials :resources :review :creation-status] id))}]
                (when (> (.indexOf [:credentials :resources :review :creation-status] current-step)
                         (.indexOf [:credentials :resources :review :creation-status] id))
                  [step-checkmark])]
               (when-not (= id (if (= current-step :creation-status) :creation-status :review))
                 [:> Box {:class "px-2"}
                  [:> Separator {:size "1" :orientation "horizontal" :class "w-4"}]])])]
      ;; Current step content
           (case current-step
             :credentials [credentials-step]
             :resources [resources-step]
             :review [review-step]
             :creation-status [creation-status-step]
             [credentials-step])]]]
        :footer-props
        {:form-type form-type
         :back-text (case current-step
                      :credentials "Back"
                      :resources "Back to Credentials"
                      :review "Back to Resources"
                      :creation-status nil)
         :next-text (case current-step
                      :credentials "Next: Resources"
                      :resources "Next: Review"
                      :review "Confirm and Create"
                      :creation-status "Go to AWS Connect")
         :on-back #(case current-step
                     :credentials (.back js/history)
                     :resources (rf/dispatch [:aws-connect/set-current-step :credentials])
                     :review (rf/dispatch [:aws-connect/set-current-step :resources])
                     :creation-status nil)
         :on-next #(case current-step
                     :credentials (rf/dispatch [:aws-connect/validate-credentials])
                     :resources (rf/dispatch [:aws-connect/set-current-step :review])
                     :review (rf/dispatch [:aws-connect/create-connections])
                     :creation-status (rf/dispatch [:navigate :integrations-aws-connect]))
         :back-hidden? (case current-step
                         :credentials false
                         :resources false
                         :review false
                         :creation-status true)
         :next-disabled? (case current-step
                           :credentials (:active? loading)
                           :resources (empty? @(rf/subscribe [:aws-connect/selected-resources]))
                           :review (some empty? (vals @(rf/subscribe [:aws-connect/agent-assignments])))
                           :creation-status false)}}])))


