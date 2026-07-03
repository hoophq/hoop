(ns webapp.resources.federation.views.setup
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout Flex Heading Text Tooltip]]
   ["lucide-react" :refer [Eye EyeOff HelpCircle Info]]
   [clojure.string :as str]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.resources.federation.views.test-dialog :as test-dialog]))

(defn- section [title description & children]
  [:> Box {:class "space-y-4"}
   [:> Flex {:align "center" :gap "2"}
    [:> Heading {:as "h4" :size "3" :weight "bold" :class "text-[--gray-12]"} title]
    (when description
      [:> Tooltip {:content description}
       [:> HelpCircle {:size 14 :class "text-[--gray-10]"}]])]
   (into [:> Box {:class "space-y-4"}] children)])

(defn- oauth-provider? [form]
  (= (:builtin_provider form) "gcp_oauth"))

(defn- admin-creds-field-label [form]
  (if (oauth-provider? form)
    "OAuth client credentials (JSON)"
    "Service account JSON"))

(defn- admin-creds-field-placeholder [form]
  (if (oauth-provider? form)
    "{\n  \"client_id\": \"...apps.googleusercontent.com\",\n  \"client_secret\": \"...\"\n}"
    "{\n  \"type\": \"service_account\",\n  ...\n}"))

(defn- admin-creds-field-callout [form]
  (if (oauth-provider? form)
    [:<>
     "These OAuth client credentials identify your Google OAuth app. "
     "Each user connects their own Google account; the client secret is only "
     "used by the gateway to exchange tokens and never reaches end-user sessions."]
    [:<>
     "This service account never touches end-user sessions directly. "
     "It is only used by the federation script to mint short-lived credentials for the mapped identity."]))

(defn- admin-creds-field [form credentials-editing? has-credentials? reveal-default?]
  (r/with-let [reveal? (r/atom (boolean reveal-default?))]
    (fn [form credentials-editing? has-credentials? _reveal-default?]
      (let [masked? (and has-credentials?
                         (not credentials-editing?)
                         (str/blank? (:admin_credentials_json form)))]
        [:> Box {:class "space-y-2"}
         [:> Flex {:align "center" :justify "between"}
          [:> Text {:size "2" :weight "medium" :class "text-[--gray-12]"}
           (admin-creds-field-label form)]
          [:> Box {:class "cursor-pointer text-[--gray-10] hover:text-[--gray-12]"
                   :role "button"
                   :aria-label (if @reveal? "Hide" "Show")
                   :on-click #(swap! reveal? not)}
           (if @reveal?
             [:> EyeOff {:size 16}]
             [:> Eye {:size 16}])]]

         (if masked?
           [:div {:class "rt-TextAreaRoot rt-variant-surface rt-r-size-3"}
            [:textarea {:class "rt-reset rt-TextAreaInput font-mono text-sm"
                        :rows 5
                        :disabled true
                        :read-only true
                        :value "••••••••••••••••••••••"
                        :on-change identity}]]
           [:div {:class "rt-TextAreaRoot rt-variant-surface rt-r-size-3"}
            [:textarea {:class (str "rt-reset rt-TextAreaInput font-mono text-sm"
                                    (when-not @reveal? " blur-sm select-none"))
                        :id "federation-admin-creds"
                        :rows 7
                        :required (not has-credentials?)
                        :placeholder (admin-creds-field-placeholder form)
                        :value (:admin_credentials_json form)
                        :on-change #(rf/dispatch [:federation/set-field
                                                  :admin_credentials_json
                                                  (-> % .-target .-value)])}]])

         (when masked?
           [:> Text {:size "2"
                     :class "text-[--accent-11] cursor-pointer hover:underline"
                     :on-click #(rf/dispatch [:federation/set-credentials-editing true])}
            "Replace credentials"])

         [:> Callout.Root {:color "gray" :variant "soft" :size "1" :class "items-center"}
          [:> Callout.Icon [:> Info {:size 14}]]
          [:> Callout.Text {:size "1"}
           (admin-creds-field-callout form)]]]))))

(defn- identity-mapping-editor [form]
  [:> Box {:class "space-y-4"}
   [forms/input
    {:label "Source attribute (Hoop)"
     :helper-text "JSONPath into the Hoop session context."
     :placeholder "$.user.email"
     :not-margin-bottom? true
     :required true
     :value (:identity_source_attribute form)
     :on-change #(rf/dispatch [:federation/set-field :identity_source_attribute (-> % .-target .-value)])}]

   [:> Box {:class "space-y-1"}
    [forms/input
     {:label "Target principal template (GCP)"
      :placeholder "{user.email}"
      :not-margin-bottom? true
      :required true
      :value (:identity_target_template form)
      :on-change #(rf/dispatch [:federation/set-field :identity_target_template (-> % .-target .-value)])}]
    [:> Text {:size "1" :class "text-[--gray-11]"}
     "Use a literal email or interpolate from the source. Example: "
     [:> Text {:as "span" :size "1" :class "font-mono bg-[--gray-3] px-1 py-0.5 rounded text-[--gray-12]"}
      "analyst-{user.email}@your-project.iam.gserviceaccount.com"]
     "."]]

   [forms/select
    {:label "Fallback method"
     :helper-text (str "What happens when a user's identity can't be mapped to a "
                       "cloud principal at session start. \"Deny the session\" blocks "
                       "access; \"Use the connection's static credentials\" runs the "
                       "session on the credentials stored on the connection.")
     :not-margin-bottom? true
     :options [{:value "deny" :text "Deny the session"}
               {:value "static" :text "Use the connection's static credentials"}]
     :selected (:fallback_policy form)
     :full-width? true
     :on-change #(rf/dispatch [:federation/set-field :fallback_policy %])}]])

(defn- env-var-row [var-name]
  [:> Flex {:align "center"}
   [:> Text {:size "2" :class "font-mono text-[--accent-11]"} var-name]
   [:> Text {:size "2" :class "font-mono text-[--gray-11]"} "=<issued at session start>"]])

(defn- output-preview-section [form]
  [:> Box {:class "space-y-3"}
   [:> Box {:class "border border-[--gray-6] rounded-md p-4 bg-[--gray-2] space-y-2"}
    (if (oauth-provider? form)
      [:<>
       [env-var-row "HOOP_GCP_ACCESS_TOKEN"]
       [env-var-row "CLOUDSDK_CORE_PROJECT"]
       [env-var-row "HOOP_FEDERATED_PRINCIPAL"]]
      [:<>
       [env-var-row "GOOGLE_APPLICATION_CREDENTIALS"]
       [env-var-row "CLOUDSDK_CORE_PROJECT"]
       [env-var-row "HOOP_FEDERATED_PRINCIPAL"]])]
   [:> Text {:size "1" :class "text-[--gray-11]"}
    "Hoop guarantees these names exist for every successful session. "
    "Your queries can rely on them the same way they would with statically configured credentials."]])

(def ^:private provider-options
  [{:value "gcp_iam" :text "GCP IAM (service account impersonation)"}
   {:value "gcp_oauth" :text "GCP OAuth (per-user Google account)"}])

(defn- provider-selector [form]
  [forms/select
   {:label "Federation provider"
    :helper-text (str "GCP IAM impersonates a service account at session time. "
                      "GCP OAuth mints session tokens from each user's own Google "
                      "account — no service accounts, but every user must connect "
                      "their account once.")
    :not-margin-bottom? true
    :options provider-options
    :selected (or (:builtin_provider form) "gcp_iam")
    :full-width? true
    :on-change (fn [provider]
                 (rf/dispatch [:federation/set-field :builtin_provider provider])
                 ;; gcp_oauth has no static service-account fallback to run a
                 ;; session on, so force the deny policy when switching to it.
                 (when (= provider "gcp_oauth")
                   (rf/dispatch [:federation/set-field :fallback_policy "deny"])))}])

(defn- oauth-consent-section [connection-name]
  [:> Box {:class "space-y-3"}
   (if (str/blank? connection-name)
     [:> Callout.Root {:color "blue" :variant "soft" :size "1" :class "items-center"}
      [:> Callout.Icon [:> Info {:size 14}]]
      [:> Callout.Text {:size "1"}
       "Save the connection first. Then each user — including you — connects "
       "their own Google account from this screen before running a session."]]

     [:<>
      [:> Flex {:gap "3" :align "center"}
       [:> Button {:variant "solid"
                   :type "button"
                   :on-click #(rf/dispatch [:federation/oauth-connect connection-name])}
        "Connect Google account"]
       [:> Button {:variant "soft"
                   :color "gray"
                   :type "button"
                   :on-click #(rf/dispatch [:federation/oauth-disconnect connection-name])}
        "Disconnect"]]
      [:> Callout.Root {:color "gray" :variant "soft" :size "1" :class "items-center"}
       [:> Callout.Icon [:> Info {:size 14}]]
       [:> Callout.Text {:size "1"}
        "This connects the Google account of the currently signed-in user. "
        "Every user who runs this connection must connect their own account once; "
        "the consented Google identity becomes the session's audited principal."]]])])

(defn main [{:keys [connection-name conn-data embedded?]}]
  (r/with-let [status-sub (rf/subscribe [:federation/status])
               data-sub (rf/subscribe [:federation/data])
               form-sub (rf/subscribe [:federation/form])
               credentials-editing?-sub (rf/subscribe [:federation/credentials-editing?])]
    (fn []
      (let [status @status-sub
            data @data-sub
            form @form-sub
            credentials-editing? @credentials-editing?-sub
            oauth? (oauth-provider? form)
            has-credentials? (:has_admin_credentials data)
            has-admin-creds? (or (not (str/blank? (:admin_credentials_json form)))
                                 has-credentials?)
            can-test? (and has-admin-creds?
                           (not (str/blank? (get-in form [:extra_config :project_id])))
                           (not (str/blank? (:identity_source_attribute form)))
                           (not (str/blank? (:identity_target_template form)))
                           (not (str/blank? (:agent_id conn-data))))
            ;; embedded inside the wizard's roles-step <form>: a nested <form>
            ;; is invalid HTML and breaks the outer form's submit
            wrapper (if embedded?
                      [:> Box {:class "space-y-8 w-full"}]
                      [:form {:id "federation-form"
                              :on-submit (fn [e] (.preventDefault e))
                              :class "space-y-8 w-full"}])]

        (conj wrapper

         [:> Flex {:justify "between" :align "center"}
          [:> Heading {:as "h3" :size "5" :weight "bold" :class "text-[--gray-12]"}
           "Federation setup"]
          ;; The dry-run "Test as user" path mints credentials via the admin
          ;; service account, which only exists for gcp_iam. gcp_oauth sessions
          ;; are validated by each user connecting their own Google account, so
          ;; the test button is hidden for that provider.
          (when-not oauth?
            [:> Tooltip {:content (cond
                                    (not has-admin-creds?) "Add service account credentials to enable testing."
                                    (str/blank? (get-in form [:extra_config :project_id])) "Set the GCP Project ID to enable testing."
                                    (str/blank? (:identity_target_template form)) "Set the target principal template to enable testing."
                                    (str/blank? (:agent_id conn-data)) "Select an agent to enable testing."
                                    :else "Run a dry-run test as a specific Hoop user")}
             [:> Button {:variant "soft"
                         :type "button"
                         :disabled (not can-test?)
                         :on-click #(rf/dispatch [:modal->open
                                                  {:content [test-dialog/main
                                                             {:conn-data conn-data}]
                                                   :maxWidth "540px"}])}
              "Test as user"]])]

         [section
          "Federation provider"
          "Which GCP federation strategy this connection uses to obtain per-session credentials."
          [provider-selector form]]

         [section
          (if oauth? "OAuth application credentials" "Admin role credentials")
          (if oauth?
            "The OAuth client (client_id + client_secret) the gateway uses to run the Google consent flow and exchange per-user tokens."
            "The service account that the hook impersonates with. Must hold roles/iam.serviceAccountTokenCreator on the principals it issues credentials for.")
          [admin-creds-field form credentials-editing? has-credentials? embedded?]
          [forms/input
           {:label "GCP Project ID"
            :placeholder "e.g. my-gcp-project"
            :required true
            :not-margin-bottom? true
            :value (get-in form [:extra_config :project_id] "")
            :on-change #(rf/dispatch [:federation/set-nested-field
                                      :extra_config :project_id
                                      (-> % .-target .-value)])}]]

         (if oauth?
           [section
            "Connect your Google account"
            "gcp_oauth runs each session as the user's own Google identity. Every user connects their Google account once per connection."
            [oauth-consent-section connection-name]]

           [section
            "Identity mapping"
            "How the Hoop user's identity maps to the cloud IAM principal that gets impersonated."
            [identity-mapping-editor form]])

         [section
          "Output preview"
          "Environment variables the hook will write into the command runtime. These exist only for the duration of the session and are discarded on exit."
          [output-preview-section form]])))))
