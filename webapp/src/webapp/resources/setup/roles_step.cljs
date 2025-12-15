(ns webapp.resources.setup.roles-step
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Button Card Flex Grid Heading Link Separator
                               Text Switch Select]]
   ["lucide-react" :refer [ArrowUpRight Plus Trash2 GlobeLock FileSpreadsheet]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multi-select]
   [webapp.config :as config]
   [webapp.resources.constants :as constants]
   [webapp.resources.helpers :as helpers]
   [webapp.resources.setup.configuration-inputs :as configuration-inputs]))

(defn get-field-prefix
  "Get the prefix for a field based on its source or provider default"
  [role-index field-key]
  (let [current-source @(rf/subscribe [:resource-setup/field-source role-index field-key])
        current-provider @(rf/subscribe [:resource-setup/secrets-manager-provider role-index])
        actual-source (or current-source
                         (if (= current-provider "aws-secrets-manager")
                           "aws-secrets-manager"
                           "vault-kv1"))]
    (helpers/get-secret-prefix actual-source)))

(defn source-selector [role-index field-key]
  (let [open? (r/atom false)]
    (fn []
      (let [field-source @(rf/subscribe [:resource-setup/field-source role-index field-key])
            secrets-provider @(rf/subscribe [:resource-setup/secrets-manager-provider role-index])
            actual-source (or field-source
                             (if (= secrets-provider "aws-secrets-manager")
                               "aws-secrets-manager"
                               "vault-kv1"))
            all-sources [{:value "vault-kv1" :text "Vault V1"}
                         {:value "vault-kv2" :text "Vault V2"}
                         {:value "aws-secrets-manager" :text "AWS Secrets Manager"}
                         {:value "manual-input" :text "Manual"}]
            available-sources (if (= secrets-provider "aws-secrets-manager")
                                (let [aws-source (first (filter #(= (:value %) "aws-secrets-manager") all-sources))
                                      other-sources (remove #(= (:value %) "aws-secrets-manager") all-sources)]
                                  (cons aws-source other-sources))
                                all-sources)
            selected-text (some #(when (= (:value %) actual-source) (:text %)) available-sources)]
        [:> Select.Root {:value actual-source
                         :open @open?
                         :onOpenChange #(reset! open? %)
                         :onValueChange (fn [new-source]
                                          (reset! open? false)
                                          (rf/dispatch [:resource-setup->update-field-source
                                                        role-index
                                                        field-key
                                                        new-source]))}
         [:> Select.Trigger {:variant "ghost"
                             :size "1"
                             :class "border-none shadow-none text-xsm font-medium text-[--gray-11]"
                             :placeholder (or selected-text "Vault V1")}]
         [:> Select.Content
          (for [source available-sources]
            ^{:key (:value source)}
            [:> Select.Item {:value (:value source)} (:text source)])]]))))

;; SSH role form - Based on server.cljs
(defn ssh-role-form [role-index]
  (let [configs (constants/get-role-config "application" "ssh")
        credentials @(rf/subscribe [:resource-setup/role-credentials role-index])
        connection-method @(rf/subscribe [:resource-setup/role-connection-method role-index])
        ;; Local state for auth method (default to password)
        auth-method (or (get credentials "auth-method") "password")
        filtered-fields (filter (fn [field]
                                  (case auth-method
                                    "password" (not= (:key field) "authorized_server_keys")
                                    "key" (not= (:key field) "pass")
                                    true))
                                configs)]
    [:> Box {:class "space-y-4"}
     ;; Authentication Method Selector
     [:> Box {:class "space-y-4 mb-6"}
      [:> Heading {:as "h4" :size "3" :weight "medium"}
       "Authentication Method"]
      [:> Grid {:columns "2" :gap "3"}
       [:> Button {:size "2"
                   :type "button"
                   :variant (if (= auth-method "password") "solid" "outline")
                   :on-click #(rf/dispatch [:resource-setup->update-role-credentials
                                            role-index
                                            "auth-method"
                                            "password"])}
        "Username & Password"]
       [:> Button {:size "2"
                   :type "button"
                   :variant (if (= auth-method "key") "solid" "outline")
                   :on-click #(rf/dispatch [:resource-setup->update-role-credentials
                                            role-index
                                            "auth-method"
                                            "key"])}
        "Private Key Authentication"]]]

     ;; SSH Fields (filtered based on auth method)
     [:> Grid {:columns "1" :gap "4"}
      (for [field filtered-fields]
        ^{:key (:key field)}
        (let [field-key (:key field)
              field-value (get credentials field-key "")
              show-source-selector? (= connection-method "secrets-manager")
              display-value field-value
              handle-change (fn [e]
                              (let [new-value (-> e .-target .-value)]
                                (rf/dispatch [:resource-setup->update-role-credentials
                                              role-index
                                              field-key
                                              new-value])))
              base-props {:label (:label field)
                          :placeholder (or (:placeholder field) (str "e.g. " field-key))
                          :value display-value
                          :required (:required field)
                          :on-change handle-change}]
          (if (= (:type field) "textarea")
            [forms/textarea base-props]
            (if show-source-selector?
              [forms/input-with-adornment (assoc base-props :type "password"
                                                 :start-adornment [source-selector role-index field-key])]
              [forms/input (assoc base-props :type "password")]))))]]))

;; TCP role form - Based on network.cljs
(defn tcp-role-form [role-index]
  (let [configs (constants/get-role-config "application" "tcp")
        credentials @(rf/subscribe [:resource-setup/role-credentials role-index])
        connection-method @(rf/subscribe [:resource-setup/role-connection-method role-index])]
    [:> Grid {:columns "1" :gap "4"}
     (for [field configs]
       ^{:key (:key field)}
       (let [field-key (:key field)
             field-value (get credentials field-key "")
             show-source-selector? (= connection-method "secrets-manager")
             display-value field-value
             handle-change (fn [e]
                             (let [new-value (-> e .-target .-value)]
                               (rf/dispatch [:resource-setup->update-role-credentials
                                             role-index
                                             field-key
                                             new-value])))]
         (if show-source-selector?
           [forms/input-with-adornment {:label (:label field)
                                        :placeholder (or (:placeholder field) (str "e.g. " field-key))
                                        :value display-value
                                        :required (:required field)
                                        :type "password"
                                        :on-change handle-change
                                        :start-adornment [source-selector role-index field-key]}]
           [forms/input {:label (:label field)
                         :placeholder (or (:placeholder field) (str "e.g. " field-key))
                         :value display-value
                         :required (:required field)
                         :type "password"
                         :on-change handle-change}])))]))

;; Kubernetes Token role form - Based on network.cljs
(defn kubernetes-token-role-form [role-index]
  (let [credentials @(rf/subscribe [:resource-setup/role-credentials role-index])
        connection-method @(rf/subscribe [:resource-setup/role-connection-method role-index])
        show-selector? (= connection-method "secrets-manager")
        remote-url-value (get credentials "remote_url" "")
        auth-token-value (get credentials "header_Authorization" "")
        auth-token-display-value (if (cs/starts-with? auth-token-value "Bearer ")
                                   (subs auth-token-value 7)
                                   auth-token-value)]
    (when (nil? (get credentials "insecure"))
      (rf/dispatch [:resource-setup->update-role-credentials
                    role-index
                    "insecure"
                    false]))

    [:> Box {:class "space-y-4"}
     ;; Cluster URL
     (if show-selector?
       [forms/input-with-adornment {:label "Cluster URL"
                                    :placeholder "e.g. https://example.com:51434"
                                    :value remote-url-value
                                    :required true
                                    :type "text"
                                    :on-change (fn [e]
                                                 (let [new-value (-> e .-target .-value)]
                                                   (rf/dispatch [:resource-setup->update-role-credentials
                                                                 role-index
                                                                 "remote_url"
                                                                 new-value])))
                                    :start-adornment [source-selector role-index "remote_url"]}]
       [forms/input {:label "Cluster URL"
                     :placeholder "e.g. https://example.com:51434"
                     :value remote-url-value
                     :required true
                     :type "text"
                     :on-change (fn [e]
                                  (let [new-value (-> e .-target .-value)]
                                    (rf/dispatch [:resource-setup->update-role-credentials
                                                  role-index
                                                  "remote_url"
                                                  new-value])))}])

     (if show-selector?
       [forms/input-with-adornment {:label "Authorization token"
                                    :placeholder "e.g. jwt.token.example"
                                    :value auth-token-display-value
                                    :required true
                                    :type "text"
                                    :on-change (fn [e]
                                                 (let [new-value (-> e .-target .-value)
                                                       transformed-val (str "Bearer " new-value)]
                                                   (rf/dispatch [:resource-setup->update-role-credentials
                                                                 role-index
                                                                 "header_Authorization"
                                                                 transformed-val])))
                                    :start-adornment [source-selector role-index "header_Authorization"]}]
       [forms/input {:label "Authorization token"
                     :placeholder "e.g. jwt.token.example"
                     :value auth-token-display-value
                     :required true
                     :type "text"
                     :on-change (fn [e]
                                  (let [new-value (-> e .-target .-value)
                                        transformed-val (str "Bearer " new-value)]
                                    (rf/dispatch [:resource-setup->update-role-credentials
                                                  role-index
                                                  "header_Authorization"
                                                  transformed-val])))}])

     [:> Flex {:align "center" :gap "3"}
      [:> Switch {:checked (get credentials "insecure" false)
                  :size "3"
                  :onCheckedChange #(rf/dispatch [:resource-setup->update-role-credentials
                                                  role-index
                                                  "insecure"
                                                  %])}]
      [:> Box
       [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12]"}
        "Allow insecure SSL"]
       [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
        "Skip SSL certificate verification for HTTPS connections."]]]]))

(defn http-proxy-role-form [role-index]
  (let [credentials @(rf/subscribe [:resource-setup/role-credentials role-index])
        connection-method @(rf/subscribe [:resource-setup/role-connection-method role-index])
        remote-url-value (get credentials "remote_url" "")
        show-selector? (= connection-method "secrets-manager")
        handle-remote-url-change (fn [e]
                                   (let [new-value (-> e .-target .-value)]
                                     (rf/dispatch [:resource-setup->update-role-credentials
                                                   role-index
                                                   "remote_url"
                                                   new-value])))]
    (when (nil? (get credentials "insecure"))
      (rf/dispatch [:resource-setup->update-role-credentials
                    role-index
                    "insecure"
                    false]))
      [:> Box {:class "space-y-4"}
       ;; Remote URL
       (if show-selector?
         [forms/input-with-adornment {:label "Remote URL"
                                      :placeholder "e.g. http://example.com"
                                      :value remote-url-value
                                      :required true
                                      :type "text"
                                      :on-change handle-remote-url-change
                                      :start-adornment [source-selector role-index "remote_url"]}]
         [forms/input {:label "Remote URL"
                       :placeholder "e.g. http://example.com"
                       :value remote-url-value
                       :required true
                       :type "text"
                       :on-change handle-remote-url-change}])

       ;; HTTP headers section (usando configuration-inputs)
       [configuration-inputs/environment-variables-section role-index
        {:title "HTTP headers"
         :subtitle "Add HTTP headers that will be used in your requests."}]

       [:> Flex {:align "center" :gap "3"}
        [:> Switch {:checked (get credentials "insecure" false)
                    :size "3"
                    :onCheckedChange #(rf/dispatch [:resource-setup->update-role-credentials
                                                    role-index
                                                    "insecure"
                                                    %])}]
        [:> Box
         [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12]"}
          "Allow insecure SSL"]
         [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
          "Skip SSL certificate verification for HTTPS connections."]]]]))


;; Custom/Metadata-driven role form (includes databases)
(defn metadata-driven-role-form [role-index]
  (let [connection @(rf/subscribe [:resource-setup/current-connection-metadata])
        credentials-config (get-in connection [:resourceConfiguration :credentials])
        metadata-credentials @(rf/subscribe [:resource-setup/metadata-credentials role-index])
        config-files @(rf/subscribe [:resource-setup/role-config-files role-index])
        config-files-map (into {} (map (fn [{:keys [key value]}] [key value]) config-files))
        connection-method @(rf/subscribe [:resource-setup/role-connection-method role-index])
        is-aws-iam-role? (= connection-method "aws-iam-role")
        is-secrets-manager? (= connection-method "secrets-manager")]
    (when (seq credentials-config)
      [:> Box
       [:> Grid {:columns "1" :gap "4"}
        (for [field credentials-config]
          (let [sanitized-name (cs/capitalize
                                (cs/lower-case
                                 (cs/replace (:name field) #"[^a-zA-Z0-9]" " ")))
                env-var-name (:name field)
                field-type (:type field)
                is-filesystem? (= field-type "filesystem")
                is-password? (= env-var-name "PASS")
                should-hide? (and is-aws-iam-role? is-password?)]

            (when-not should-hide?
              (let [field-value (if is-filesystem?
                                  (get config-files-map env-var-name "")
                                  (get metadata-credentials env-var-name ""))
                    display-type (case field-type
                                   "filesystem" "textarea"
                                   "textarea" "textarea"
                                   "password")
                    show-source-selector? is-secrets-manager?
                    display-value field-value
                    handle-change (fn [e]
                                    (let [new-value (-> e .-target .-value)
                                          actual-prefix (if is-secrets-manager?
                                                          (get-field-prefix role-index env-var-name)
                                                          "")]
                                      (if is-filesystem?
                                        (rf/dispatch [:resource-setup->update-role-config-file-by-key
                                                      role-index
                                                      env-var-name
                                                      new-value])
                                        (rf/dispatch [:resource-setup->update-role-metadata-credentials
                                                      role-index
                                                      env-var-name
                                                      new-value
                                                      actual-prefix]))))]
                ^{:key env-var-name}
                (if (= display-type "textarea")
                  [forms/textarea {:label sanitized-name
                                   :placeholder (or (:placeholder field) (:description field))
                                   :value display-value
                                   :required (:required field)
                                   :helper-text (:description field)
                                   :on-change handle-change}]
                  (if show-source-selector?
                    [forms/input-with-adornment {:label sanitized-name
                                                 :placeholder (or (:placeholder field) (:description field))
                                                 :value display-value
                                                 :required (:required field)
                                                 :type display-type
                                                 :helper-text (:description field)
                                                 :on-change handle-change
                                                 :start-adornment [source-selector role-index env-var-name]}]
                    [forms/input {:label sanitized-name
                                  :placeholder (or (:placeholder field) (:description field))
                                  :value display-value
                                  :required (:required field)
                                  :type display-type
                                  :helper-text (:description field)
                                  :on-change handle-change}]))))))]])))

;; Linux/Container role form - Based on server.cljs
(defn linux-container-role-form [role-index]
  [:> Box {:class "space-y-6"}
   ;; Environment variables section
   [configuration-inputs/environment-variables-section role-index {}]

   ;; Configuration files section
   [configuration-inputs/configuration-files-section role-index]

   ;; Additional command section
   [:> Box {:class "space-y-4"}
    [:> Heading {:as "h4" :size "3" :weight "medium"}
     "Additional command"]
    [:> Text {:size "2" :color "gray"}
     "Each argument should be entered separately."
     [:br]
     "Press Enter after each argument to add it to the list."]
    [:> Box
     [multi-select/text-input
      {:value (clj->js @(rf/subscribe [:resource-setup/role-command-args role-index]))
       :input-value @(rf/subscribe [:resource-setup/role-command-current-arg role-index])
       :on-change #(rf/dispatch [:resource-setup->set-role-command-args role-index %])
       :on-input-change #(rf/dispatch [:resource-setup->set-role-command-current-arg role-index %])
       :label "Command Arguments"
       :id "command-args"
       :name "command-args"}]
     [:> Text {:size "2" :color "gray" :mt "2"}
      "Example: 'python', '-m', 'http.server', '8000'"]]]])

(defn connection-method-card [{:keys [icon title description selected? on-click icon-class]}]
  [:> Card {:size "1"
            :variant "surface"
            :class (str "w-full cursor-pointer transition-all "
                       (if selected?
                         "before:bg-primary-12"
                         ""))
            :on-click on-click}
   [:> Flex {:align "start" :gap "3" :class (if selected? "text-[--gray-1]" "text-[--gray-12]")}
    (when icon
      [:> Avatar {:radius "large"
                  :fallback (if (fn? icon)
                              (r/as-element [icon])
                              (r/as-element [:> icon {:size 20}]))
                  :size "4"
                  :variant "soft"
                  :color "gray"
                  :class (str "flex-shrink-0 "
                              (when selected? "dark")
                              (or icon-class ""))}])
    [:> Flex {:direction "column" :gap "1"}
     [:> Text {:size "3" :weight "medium" :class (if selected? "text-[--gray-1]" "text-[--gray-12]")}
      title]
     [:> Text {:size "2" :class (if selected? "text-[--gray-1]" "text-[--gray-11]")}
      description]]]])

(defn role-connection-method-selector [role-index]
  (let [connection-method @(rf/subscribe [:resource-setup/role-connection-method role-index])
        resource-subtype @(rf/subscribe [:resource-setup/resource-subtype])
        supports-aws-iam? (contains? #{"mysql" "postgres"} resource-subtype)]

    [:> Box {:class "space-y-3"}
     [connection-method-card
      {:icon FileSpreadsheet
       :title "Manual Input"
       :description "Enter credentials directly, including host, user, password, and other connection details."
       :selected? (= connection-method "manual-input")
       :on-click #(rf/dispatch [:resource-setup->update-role-connection-method
                                role-index
                                "manual-input"])}]
     [connection-method-card
      {:icon GlobeLock
       :title "Secrets Manager"
       :description "Connect to a secrets provider like AWS Secrets Manager or HashiCorp Vault to automatically fetch your resource credentials."
       :selected? (= connection-method "secrets-manager")
       :on-click #(rf/dispatch [:resource-setup->update-role-connection-method
                                role-index
                                "secrets-manager"])}]
     (when supports-aws-iam?
       (let [aws-icon (fn [] (r/as-element
                              [:> Box {:class "w-5 h-5 flex items-center justify-center"}
                               [:img {:role "aws-icon"
                                      :src (str config/webapp-url "/icons/automatic-resources/aws.svg")
                                      :class "w-full h-full"
                                      :alt "AWS"}]]))
             icon-class (when (= connection-method "aws-iam-role") "brightness-0 invert")]
         [connection-method-card
          {:icon aws-icon
           :icon-class icon-class
           :title "AWS IAM Role"
           :description "Use an IAM Role that can be assumed to authenticate and access AWS resources."
           :selected? (= connection-method "aws-iam-role")
           :on-click #(rf/dispatch [:resource-setup->update-role-connection-method
                                    role-index
                                    "aws-iam-role"])}]))]))

(defn secrets-manager-provider-selector [role-index]
  (let [provider @(rf/subscribe [:resource-setup/secrets-manager-provider role-index])]
    [:> Box
     [forms/select {:label "Secrets manager provider"
                    :options [{:value "vault-kv1" :text "HashiCorp Vault"}
                              {:value "aws-secrets-manager" :text "AWS Secrets Manager"}]
                    :selected provider
                    :full-width? true
                    :not-margin-bottom? true
                    :on-change #(rf/dispatch [:resource-setup->update-secrets-manager-provider
                                              role-index
                                              %])}]

     [:> Flex {:align "center" :gap "1" :mt "1"}
      [:> Text {:size "2" :class "text-[--gray-11]"}
       (str "Learn more about " (if (= provider "aws-secrets-manager")
                                  "AWS Secrets Manager"
                                  "HashiCorp Vault") " setup in")]
      [:> Link {:href (get-in config/docs-url [:setup :configuration :secrets-manager])
                :target "_blank"
                :class "inline-flex items-center"}
       [:> Text {:size "2"}
        "our Docs ↗"]]]]))

(defn aws-iam-role-section [_role-index]
  [:> Flex {:align "center" :gap "1" :mt "1"}
   [:> Text {:size "2" :class "text-[--gray-11]"}
    "Learn more about AWS IAM Role setup in"]
   [:> Link {:href (get-in config/docs-url [:setup :configuration :aws-iam-role])
             :target "_blank"
             :class "inline-flex items-center"}
    [:> Text {:size "2"}
     "our Docs ↗"]]])

;; Single role configuration
(defn role-configuration [role-index]
  (let [roles @(rf/subscribe [:resource-setup/roles])
        role (get roles role-index)
        resource-subtype @(rf/subscribe [:resource-setup/resource-subtype])
        can-remove? (> (count roles) 1)
        connection-method @(rf/subscribe [:resource-setup/role-connection-method role-index])]

    [:> Grid {:columns "7" :gap "7"}
     ;; Left side - "New Role" description
     [:> Box {:grid-column "span 3 / span 3"}
      [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
       "New Role"]
      [:> Text {:size "2" :class "text-[--gray-11]"}
       "Fill out the information to access your Resource with this specific Role."]]

     ;; Right side - Form fields
     [:> Box {:grid-column "span 4 / span 4" :class "space-y-8"}
      ;; Role Information section
      [:> Box
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12] mb-3"}
        "Role information"]
       [forms/input {:label "Name"
                     :placeholder "e.g. read-only"
                     :value (:name role)
                     :required true
                     :on-change #(rf/dispatch [:resource-setup->update-role-name
                                               role-index
                                               (-> % .-target .-value)])}]]
      [:> Box {:class "space-y-6"}
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12] mb-3"}
        "Role connection method"]
       [role-connection-method-selector role-index]]


      (cond
        (= connection-method "secrets-manager")
        [secrets-manager-provider-selector role-index]

        (= connection-method "aws-iam-role")
        [aws-iam-role-section role-index])


      (cond
        (= resource-subtype "ssh")
        [ssh-role-form role-index]

        (= resource-subtype "tcp")
        [tcp-role-form role-index]

        (= resource-subtype "httpproxy")
        [http-proxy-role-form role-index]

        (= resource-subtype "linux-vm")
        [linux-container-role-form role-index]

        (= resource-subtype "kubernetes-token")
        [kubernetes-token-role-form role-index]

        :else
        [metadata-driven-role-form role-index])

      ;; Remove role button (only if more than one role)
      (when can-remove?
        [:> Flex {:justify "end"}
         [:> Button {:size "2"
                     :type "button"
                     :variant "ghost"
                     :color "red"
                     :on-click #(rf/dispatch [:resource-setup->remove-role role-index])}
          [:> Trash2 {:size 16}]
          "Remove Role"]])]]))

;; Main roles step component
(defn main []
  (let [roles @(rf/subscribe [:resource-setup/roles])
        context @(rf/subscribe [:resource-setup/context])]

    [:form {:id "roles-form"
            :on-submit (fn [e]
                         (.preventDefault e)
                         ;; Use different submit event based on context
                         (if (= context :add-role)
                           (rf/dispatch [:add-role->submit])
                           (rf/dispatch [:resource-setup->submit])))}
     [:> Box {:class "p-8 space-y-16"}
      ;; Header
      [:> Box {:class "space-y-2"}
       [:> Heading {:as "h2" :size "6" :weight "bold" :class "text-gray-12"}
        "Setup your Resource roles"]
       [:> Text {:as "p" :size "3" :class "text-gray-12"}
        "Roles are the central concept in Hoop.dev that serve as secure bridges between users and your organization's resources. They enable controlled access to internal services, databases, and other resources while maintaining security and compliance."]
       [:> Text {:as "p" :size "2" :class "text-gray-11 flex items-center gap-1"}
        "Access"
        [:> Flex {:align "center" :gap "1"}
         [:> Link {:href "https://hoop.dev/docs/"
                   :target "_blank"}
          " our Docs"]
         [:> ArrowUpRight {:size 12 :class "text-primary-11"}]]
        " to learn more about Roles."]]

      ;; Render all roles
      (if (empty? roles)
        ;; No roles yet - auto add first one
        (do
          (rf/dispatch [:resource-setup->add-role])
          [:> Box])

        ;; Render existing roles
        [:<>
         (for [role-index (range (count roles))]
           ^{:key role-index}
           [:<>
            [role-configuration role-index]

            ;; Add separator between roles (only between roles, not at the end)
            (when (< role-index (dec (count roles)))
              [:> Box {:class "my-16"}
               [:> Separator {:size "4"}]])])

         ;; Add another role button at the end
         [:> Grid {:columns "7" :gap "7"}
          [:> Box {:grid-column "span 4 / span 4" :grid-column-start "4"}
           [:> Button {:size "2"
                       :variant "soft"
                       :type "button"
                       :on-click #(rf/dispatch [:resource-setup->add-role])}
            [:> Plus {:size 16}]
            "Add New Role"]]]])]]))
