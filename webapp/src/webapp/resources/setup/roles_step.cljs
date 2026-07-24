(ns webapp.resources.setup.roles-step
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout Flex Grid Heading Link RadioGroup Separator Text Switch]]
   ["lucide-react" :refer [ArrowUpRight Check Plus ShieldCheck Trash2]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multi-select]
   [webapp.resources.constants :as constants]
   [webapp.resources.setup.configuration-inputs :as configuration-inputs]
   [webapp.resources.setup.connection-method :as connection-method]))


;; SSH role form - Based on server.cljs
(defn ssh-role-form [role-index]
  (let [configs (constants/get-role-config "application" "ssh")
        credentials @(rf/subscribe [:resource-setup/role-credentials role-index])
        connection-method @(rf/subscribe [:resource-setup/role-connection-method role-index])
        ;; Local state for auth method (default to password)
        auth-method (or (get credentials "auth-method") "password")
        ;; connection-type selects proxy (default) vs local; stored alongside the
        ;; role credentials but stripped before the payload is built.
        connection-type (or (get credentials "connection-type") "proxy")
        local? (= connection-type "local")
        filtered-fields (filter (fn [field]
                                  (case auth-method
                                    "password" (not= (:key field) "authorized_server_keys")
                                    "key" (not= (:key field) "pass")
                                    true))
                                configs)]
    [:> Box {:class "space-y-4"}
     ;; Connection Type: proxy (default) vs local
     [:> Box {:class "space-y-4 mb-6"}
      [:> Heading {:as "h4" :size "3" :weight "medium"}
       "Connection Type"]
      [:> RadioGroup.Root
       {:value connection-type
        :on-value-change #(rf/dispatch [:resource-setup->update-role-credentials
                                        role-index "connection-type" %])}
       [:> Flex {:direction "column" :gap "3"}
        [:> Box
         [:> RadioGroup.Item {:value "proxy"} "Proxy to a remote host"]
         [:> Text {:as "p" :size "2" :color "gray" :ml "5"}
          "The agent authenticates to a remote SSH server and forwards the session. Configure the target host and credentials below."]]
        [:> Box
         [:> RadioGroup.Item {:value "local"} "Local (run on the agent host)"]
         [:> Text {:as "p" :size "2" :color "gray" :ml "5"}
          "The agent runs the shell or command directly on the machine where it is deployed. No target host or credentials are required."]]]]]

     ;; Credential configuration is only relevant when proxying to a remote host.
     (when-not local?
       [:<>
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
                             :type "password"
                             :on-change handle-change
                             :start-adornment (when show-source-selector?
                                                [connection-method/source-selector role-index field-key])}]
             (if (= (:type field) "textarea")
               [forms/textarea (dissoc base-props :type :start-adornment)]
               [forms/input base-props])))]])]))

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
         [forms/input {:label (:label field)
                       :placeholder (or (:placeholder field) (str "e.g. " field-key))
                       :value display-value
                       :required (:required field)
                       :type "password"
                       :on-change handle-change
                       :start-adornment (when show-source-selector?
                                          [connection-method/source-selector role-index field-key])}]))]))

;; Kubernetes Token role form - Based on network.cljs
(defn kubernetes-token-role-form [role-index]
  (let [credentials @(rf/subscribe [:resource-setup/role-credentials role-index])
        connection-method @(rf/subscribe [:resource-setup/role-connection-method role-index])
        show-selector? (= connection-method "secrets-manager")
        auth-token-source @(rf/subscribe [:resource-setup/field-source role-index "header_Authorization"])
        remote-url-value (get credentials "remote_url" "")
        auth-token-value (get credentials "header_Authorization" "")
        insecure-value (get credentials "insecure" false)
        auth-token-display-value (if (cs/starts-with? auth-token-value "Bearer ")
                                   (subs auth-token-value 7)
                                   auth-token-value)
        is-auth-manual-input? (= auth-token-source "manual-input")]
    (when (nil? (get credentials "insecure"))
      (rf/dispatch [:resource-setup->update-role-credentials
                    role-index
                    "insecure"
                    false]))

    [:> Box {:class "space-y-4"}
     ;; Cluster URL
     [forms/input {:label "Cluster URL"
                   :placeholder "e.g. https://kubernetes.default.svc.cluster.local:443"
                   :value remote-url-value
                   :required true
                   :type "text"
                   :on-change (fn [e]
                                (let [new-value (-> e .-target .-value)]
                                  (rf/dispatch [:resource-setup->update-role-credentials
                                                role-index
                                                "remote_url"
                                                new-value])))
                   :start-adornment (when show-selector?
                                      [connection-method/source-selector role-index "remote_url"])}]

     [forms/input {:label "Authorization token"
                   :placeholder "e.g. jwt.token.example"
                   :value auth-token-display-value
                   :required true
                   :type "text"
                   :on-change (fn [e]
                                (let [new-value (-> e .-target .-value)
                                      transformed-val (if is-auth-manual-input?
                                                        (if (cs/starts-with? new-value "Bearer ")
                                                          new-value
                                                          (str "Bearer " new-value))
                                                        new-value)]
                                  (rf/dispatch [:resource-setup->update-role-credentials
                                                role-index
                                                "header_Authorization"
                                                transformed-val])))
                   :start-adornment (when show-selector?
                                      [connection-method/source-selector role-index "header_Authorization"])}]

     [:> Flex {:align "center" :gap "3"}
      [:> Switch {:checked insecure-value
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
     [forms/input {:label "Remote URL"
                   :placeholder "e.g. http://example.com"
                   :value remote-url-value
                   :required true
                   :type "text"
                   :on-change handle-remote-url-change
                   :start-adornment (when show-selector?
                                      [connection-method/source-selector role-index "remote_url"])}]

     ;; HTTP headers section
     [configuration-inputs/http-headers-section role-index]

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

(defn- claude-code-cred-display
  "Display value for a claude-code create-form field, unwrapping the
  {:value :source} secrets-manager shape into a plain string."
  [credentials k]
  (let [v (get credentials k "")]
    (if (map? v) (or (:value v) "") v)))

(defn claude-code-role-form [role-index]
  (let [creds @(rf/subscribe [:resource-setup/role-credentials role-index])
        connection-method @(rf/subscribe [:resource-setup/role-connection-method role-index])
        vertex-flag? @(rf/subscribe [:feature-flag/enabled? "experimental.claude_code_vertex"])
        provider (let [p (claude-code-cred-display creds "provider")]
                   (if (empty? p) "anthropic" p))
        vertex? (= provider "vertex")
        ;; Show the Vertex option when the feature is enabled, or when the
        ;; role is already set to Vertex (so it never hides existing config).
        show-vertex-option? (or vertex-flag? vertex?)
        api-url-value (claude-code-cred-display creds "remote_url")
        api-key-value (claude-code-cred-display creds "HEADER_X_API_KEY")
        region-value (claude-code-cred-display creds "GCP_REGION")
        project-value (claude-code-cred-display creds "GCP_PROJECT_ID")
        sa-json-value (claude-code-cred-display creds "GCP_SERVICE_ACCOUNT_JSON")
        show-selector? (= connection-method "secrets-manager")
        update-cred (fn [k]
                      (fn [e]
                        (rf/dispatch [:resource-setup->update-role-credentials
                                      role-index k (-> e .-target .-value)])))]

    ;; Initialize default values. REMOTE_URL is only relevant for the
    ;; Anthropic provider; Vertex derives it from the region at submit time.
    (when (and (not vertex?) (empty? api-url-value))
      (rf/dispatch [:resource-setup->update-role-credentials
                    role-index
                    "remote_url"
                    "https://api.anthropic.com"]))

    (when (nil? (get creds "insecure"))
      (rf/dispatch [:resource-setup->update-role-credentials
                    role-index
                    "insecure"
                    false]))

    [:> Box {:class "space-y-radix-6"}
     [:> Box {:class "space-y-radix-4"}
      [:> Heading {:size "3"} "Basic info"]

      (when show-vertex-option?
        [forms/select
         {:label "Provider"
          :options [{:text "Anthropic API" :value "anthropic"}
                    {:text "Google Vertex AI" :value "vertex"}]
          :selected provider
          :on-change #(rf/dispatch [:resource-setup->update-role-credentials
                                    role-index "provider" %])}])

      (if vertex?
        [:<>
         [:> Callout.Root {:size "1" :color "gray"}
          [:> Callout.Text
           "Claude Code runs in Vertex mode against hoop. hoop mints a short-lived "
           "token from the service account below and proxies requests to Google Vertex AI."]]

         [forms/input {:label "GCP Region"
                       :placeholder "us-east5"
                       :value region-value
                       :required true
                       :type "text"
                       :on-change (update-cred "GCP_REGION")}]

         [forms/input {:label "GCP Project ID"
                       :placeholder "my-gcp-project"
                       :value project-value
                       :required true
                       :type "text"
                       :on-change (update-cred "GCP_PROJECT_ID")}]

         [forms/textarea {:label "Service Account JSON"
                          :placeholder "{\n  \"type\": \"service_account\",\n  ...\n}"
                          :value sa-json-value
                          :required true
                          :rows 8
                          :on-change (update-cred "GCP_SERVICE_ACCOUNT_JSON")}]]

        [:<>
         [forms/input {:label "Anthropic API URL"
                       :placeholder "https://api.anthropic.com"
                       :value (if (empty? api-url-value) "https://api.anthropic.com" api-url-value)
                       :required true
                       :type "text"
                       :on-change (update-cred "remote_url")
                       :start-adornment (when show-selector?
                                          [connection-method/source-selector role-index "remote_url"])}]

         [forms/input {:label "Anthropic API Key"
                       :placeholder "sk-ant-..."
                       :value api-key-value
                       :required true
                       :type "password"
                       :on-change (update-cred "HEADER_X_API_KEY")
                       :start-adornment (when show-selector?
                                          [connection-method/source-selector role-index "HEADER_X_API_KEY"])}]])]

     [configuration-inputs/http-headers-section role-index]

     [:> Flex {:align "center" :gap "3"}
      [:> Switch {:checked (get creds "insecure" false)
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


;; MCP role form - HTTP proxy subtype whose endpoint is protected by OAuth
;; (e.g. https://mcp.figma.com/mcp). The admin resolves the MCP authorization
;; here at setup: Hoop drives the OAuth login and freezes the obtained access
;; token into the connection's HEADER_AUTHORIZATION credential.
(defn mcp-role-form [role-index]
  (let [credentials @(rf/subscribe [:resource-setup/role-credentials role-index])
        connection-method @(rf/subscribe [:resource-setup/role-connection-method role-index])
        mcp-state @(rf/subscribe [:mcp-oauth/state role-index])
        remote-url-value (get credentials "remote_url" "")
        authorized? (not (cs/blank? (get credentials "HEADER_AUTHORIZATION" "")))
        status (:status mcp-state :idle)
        busy? (contains? #{:authorizing :pending} status)
        show-selector? (= connection-method "secrets-manager")
        handle-remote-url-change (fn [e]
                                   (rf/dispatch [:resource-setup->update-role-credentials
                                                 role-index "remote_url" (-> e .-target .-value)]))]
    (when (nil? (get credentials "insecure"))
      (rf/dispatch [:resource-setup->update-role-credentials role-index "insecure" false]))
    [:> Box {:class "space-y-6"}
     ;; Server URL
     [forms/input {:label "MCP Server URL"
                   :placeholder "e.g. https://mcp.linear.app"
                   :value remote-url-value
                   :required true
                   :type "text"
                   :on-change handle-remote-url-change
                   :start-adornment (when show-selector?
                                      [connection-method/source-selector role-index "remote_url"])}]

     ;; Authorization section
     [:> Box {:class "space-y-4 rounded-md border border-[--gray-5] p-4"}
      [:> Box
       [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12]"}
        "MCP Authorization"]
       [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
        "Log in to the MCP server to obtain an access token. The token is stored in this connection's Authorization header."]]

      ;; Optional pre-registered client credentials. Left blank, Hoop registers
      ;; a client dynamically with the MCP server. These inputs are auth-flow
      ;; only and are never stored as connection environment variables.
      [:> Box {:class "space-y-3"}
       [forms/input {:label "Client ID (optional)"
                     :placeholder "Leave blank to register automatically"
                     :value (or (:client-id mcp-state) "")
                     :type "text"
                     :on-change #(rf/dispatch [:mcp-oauth/set-field role-index :client-id (-> % .-target .-value)])}]
       [forms/input {:label "Client Secret (optional)"
                     :placeholder "Only if your client requires one"
                     :value (or (:client-secret mcp-state) "")
                     :type "password"
                     :on-change #(rf/dispatch [:mcp-oauth/set-field role-index :client-secret (-> % .-target .-value)])}]]

      ;; Status + action
      (cond
        authorized?
        [:> Flex {:align "center" :justify "between" :gap "3"}
         [:> Flex {:align "center" :gap "2"}
          [:> Check {:size 16 :class "text-[--grass-11]"}]
          [:> Text {:size "2" :weight "medium" :class "text-[--grass-11]"}
           "Authorized — access token stored"]]
         [:> Flex {:gap "2"}
          [:> Button {:size "2" :type "button" :variant "soft" :disabled busy?
                      :on-click #(rf/dispatch [:mcp-oauth/authorize role-index])}
           "Re-authorize"]
          [:> Button {:size "2" :type "button" :variant "ghost" :color "red" :pt "3"
                      :on-click #(rf/dispatch [:mcp-oauth/clear role-index])}
           "Clear"]]]

        :else
        [:> Box {:class "space-y-2"}
         [:> Button {:size "2" :type "button" :variant "solid" :disabled busy?
                     :on-click #(rf/dispatch [:mcp-oauth/authorize role-index])}
          [:> ShieldCheck {:size 16}]
          (if busy? "Authorizing…" "Authorize with MCP")]
         (when (= status :error)
           [:> Text {:as "p" :size "2" :class "text-[--red-11]"}
            (or (:error mcp-state) "Authorization failed")])])]

     ;; Optional extra headers (forwarded alongside the Authorization header)
     [configuration-inputs/http-headers-section role-index]

     [:> Flex {:align "center" :gap "3"}
      [:> Switch {:checked (get credentials "insecure" false)
                  :size "3"
                  :onCheckedChange #(rf/dispatch [:resource-setup->update-role-credentials
                                                  role-index "insecure" %])}]
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
        field-source (fn [env-var-name]
                       @(rf/subscribe [:resource-setup/field-source role-index env-var-name]))
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
                                          field-source (field-source env-var-name)]
                                      (if is-filesystem?
                                        (rf/dispatch [:resource-setup->update-role-config-file-by-key
                                                      role-index
                                                      env-var-name
                                                      new-value])
                                        (rf/dispatch [:resource-setup->update-role-metadata-credentials
                                                      role-index
                                                      env-var-name
                                                      new-value
                                                      field-source]))))]
                (if (= display-type "textarea")
                  ^{:key env-var-name}
                  [forms/textarea {:label sanitized-name
                                   :placeholder (or (:placeholder field) (:description field))
                                   :value display-value
                                   :required (:required field)
                                   :helper-text (:description field)
                                   :on-change handle-change}]

                  ^{:key env-var-name}
                  [forms/input {:label sanitized-name
                                :placeholder (or (:placeholder field) (:description field))
                                :value display-value
                                :required (:required field)
                                :type display-type
                                :helper-text (:description field)
                                :on-change handle-change
                                :start-adornment (when show-source-selector?
                                                   [connection-method/source-selector role-index env-var-name])}])))))]])))

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

(defn role-attributes-field
  "Per-role Attributes selector. While a protection profile is active, its
  managed attribute appears pre-selected as a distinct blue pill. Removing
  it opts the role out of the profile (the attribute is not sent); it can
  be re-added from the dropdown before submitting. When kept, the attribute
  is included in the role's attributes at submit time."
  [role-index]
  (let [attributes-data @(rf/subscribe [:attributes/list-data])
        selected @(rf/subscribe [:resource-setup/role-attributes role-index])
        skip-profile? @(rf/subscribe [:resource-setup/role-skip-protection-profile? role-index])
        managed-pill @(rf/subscribe [:protection-profile/managed-pill])]
    [:> Box {:class "mt-4"}
     [multi-select/creatable-select
      {:id (str "role-attributes-" role-index)
       :name (str "role-attributes-" role-index)
       :label "Attributes"
       :placeholder "Select or type to create"
       ;; Managed attributes come through :managed-options with their own
       ;; styling — drop them from the regular option list to avoid duplicates.
       :options (into []
                      (comp (remove :managed_by)
                            (map #(hash-map :value (:name %) :label (:name %))))
                      attributes-data)
       :default-value (mapv #(hash-map :value % :label %) selected)
       :managed-options (when managed-pill
                          [{:value (:attribute-name managed-pill)
                            :label (:display-name managed-pill)}])
       :managed-value (when (and managed-pill (not skip-profile?))
                        [(:attribute-name managed-pill)])
       :on-managed-change (fn [managed-values]
                            (rf/dispatch [:resource-setup->set-role-skip-protection-profile
                                          role-index
                                          (empty? managed-values)]))
       :on-change (fn [selected-options]
                    (rf/dispatch [:resource-setup->update-role-attributes role-index
                                  (mapv :value (js->clj selected-options :keywordize-keys true))]))
       :on-create-option (fn [input-value]
                           (rf/dispatch [:attributes/create-inline {:name input-value}])
                           (rf/dispatch [:resource-setup->update-role-attributes role-index
                                         (conj selected input-value)]))}]
     [:> Text {:size "2" :class "text-[--gray-11]"}
      "Determine how protection rules and access policies apply to this role."]]))

(defn role-configuration [role-index]
  (let [roles @(rf/subscribe [:resource-setup/roles])
        role (get roles role-index)
        resource-subtype @(rf/subscribe [:resource-setup/resource-subtype])
        connection @(rf/subscribe [:resource-setup/current-connection-metadata])
        credentials-config (get-in connection [:resourceConfiguration :credentials])
        has-env-vars? (or (contains? #{"linux-vm"} resource-subtype)
                          (contains? constants/http-proxy-subtypes resource-subtype))
        has-credentials? (seq credentials-config)
        ;; Local SSH has no credentials, so the credential-source selector is
        ;; irrelevant and hidden.
        local-ssh? (and (= resource-subtype "ssh")
                        (= (get (:credentials role) "connection-type") "local"))
        should-show-connection-method? (and (or has-credentials? has-env-vars?)
                                            (not local-ssh?))
        can-remove? (> (count roles) 1)]

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
                                               (-> % .-target .-value)])}]
       [role-attributes-field role-index]]
      (when should-show-connection-method?
        [connection-method/main role-index])


      (cond
        (= resource-subtype "ssh")
        [ssh-role-form role-index]

        (= resource-subtype "tcp")
        [tcp-role-form role-index]

        (= resource-subtype "claude-code")
        [claude-code-role-form role-index]

        (= resource-subtype "mcp")
        [mcp-role-form role-index]

        (contains? constants/http-proxy-subtypes resource-subtype)
        [http-proxy-role-form role-index]

        (= resource-subtype "linux-vm")
        [linux-container-role-form role-index]

        (= resource-subtype "kubernetes-token")
        [kubernetes-token-role-form role-index]

        ;; BigQuery with IAM federation: credentials are managed by the
        ;; federation config; no static credential form needed here.
        (and (= resource-subtype "bigquery")
             (= (:connection-method (get roles role-index)) "iam_federation"))
        nil

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
  ;; One fetch per wizard mount, regardless of how many roles are added:
  ;; the attribute catalog feeds every role's Attributes field and the
  ;; active protection profile feeds its fixed pill.
  (rf/dispatch [:attributes/list])
  (rf/dispatch [:protection-profile/fetch])
  (fn []
    (let [roles @(rf/subscribe [:resource-setup/roles])
          context @(rf/subscribe [:resource-setup/context])]

      [:form {:id "roles-form"
            :on-submit (fn [e]
                         (.preventDefault e)
                         ;; Add pending env vars for all roles before submitting
                         (doseq [[role-index role] (map-indexed vector roles)]
                           (let [current-key (:env-current-key role)
                                 current-value-map (:env-current-value role)
                                 current-value (if (map? current-value-map)
                                                 (:value current-value-map)
                                                 current-value-map)]
                             (when (and (not (cs/blank? current-key))
                                        (not (cs/blank? current-value)))
                               (rf/dispatch [:resource-setup->add-role-env-row role-index]))))
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
            "Add New Role"]]]])]])))
