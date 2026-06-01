(ns webapp.resources.federation.views.setup
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout Flex Grid Heading Text Tooltip]]
   ["lucide-react" :refer [ArrowRight ChevronDown Eye EyeOff FlaskConical HelpCircle Info]]
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

(defn- sa-json-field [form credentials-editing? has-credentials?]
  (r/with-let [reveal? (r/atom true)]
    (fn [form credentials-editing? has-credentials?]
      (let [masked? (and has-credentials?
                         (not credentials-editing?)
                         (str/blank? (:admin_credentials_json form)))]
        [:> Box {:class "space-y-2"}
         [:> Flex {:align "center" :justify "between"}
          [:> Text {:size "2" :weight "medium" :class "text-[--gray-12]"}
           "Service account JSON"]
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
                        :placeholder "{\n  \"type\": \"service_account\",\n  ...\n}"
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
           "This service account never touches end-user sessions directly. "
           "It is only used by the federation script to mint short-lived credentials for the mapped identity."]]]))))

(defn- identity-pill [text]
  [:> Box {:class "inline-flex items-center px-2 py-0.5 rounded bg-[--accent-3] text-[--accent-11] text-xs font-mono font-medium"}
   text])

(defn- source-attribute->display [source-attribute]
  (-> (or source-attribute "$.user.email")
      (str/replace #"^\$\." "")
      (#(str "{" % "}"))))

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
      "analyst-{user.id}@your-project.iam.gserviceaccount.com"]
     "."]]

   [forms/select
    {:label "Fallback when no match"
     :not-margin-bottom? true
     :options [{:value "deny" :text "Deny session"}
               {:value "readonly" :text "Read-only access"}]
     :selected (:fallback form)
     :full-width? true
     :on-change #(rf/dispatch [:federation/set-field :fallback %])}]])

(defn- identity-mapping-accordion [form open?]
  [:> Box {:class "border border-[--gray-6] rounded-md overflow-hidden"}
   [:> Box {:class "p-4 space-y-4"}
    [:> Grid {:columns "5" :gap "2" :align "center"}
     [:> Box {:grid-column "span 2 / span 2"}
      [:> Text {:size "1" :weight "bold" :class "text-[--gray-10] uppercase tracking-wide mb-2 block"}
       "HOOP USER (via Entra ID)"]
      [identity-pill (source-attribute->display (:identity_source_attribute form))]]

     [:> Flex {:justify "center" :align "center"}
      [:> ArrowRight {:size 16 :class "text-[--gray-9]"}]]

     [:> Box {:grid-column "span 2 / span 2" :class "text-right"}
      [:> Text {:size "1" :weight "bold" :class "text-[--gray-10] uppercase tracking-wide mb-2 block"}
       "GCP PRINCIPAL"]
      [identity-pill (let [tmpl (:identity_target_template form "")]
                       (if (str/blank? tmpl) "{user.email}" tmpl))]]]

    [:> Flex {:justify "start"}
     [:> Flex {:as "button"
               :align "center"
               :gap "1"
               :type "button"
               :aria-expanded open?
               :on-click #(rf/dispatch [:federation/toggle-mapping-editor])
               :class "text-[--accent-11] hover:underline cursor-pointer"}
      [:> Text {:size "1" :weight "medium"} "Edit mapping"]
      [:> ChevronDown {:size 12
                       :class (str "transition-transform"
                                   (when open? " rotate-180"))}]]]]

   (when open?
     [:> Box {:class "border-t border-[--gray-5] p-4 bg-[--gray-2]"}
      [identity-mapping-editor form]])])

(defn- env-var-row [var-name]
  [:> Flex {:align "center"}
   [:> Text {:size "2" :class "font-mono text-[--accent-11]"} var-name]
   [:> Text {:size "2" :class "font-mono text-[--gray-11]"} "=<issued at session start>"]])

(defn- output-preview-section []
  [:> Box {:class "space-y-3"}
   [:> Box {:class "border border-[--gray-6] rounded-md p-4 bg-[--gray-2] space-y-2"}
    [env-var-row "GOOGLE_APPLICATION_CREDENTIALS"]
    [env-var-row "CLOUDSDK_CORE_PROJECT"]
    [env-var-row "HOOP_FEDERATED_PRINCIPAL"]]
   [:> Text {:size "1" :class "text-[--gray-11]"}
    "Hoop guarantees these names exist for every successful session. "
    "Your queries can rely on them the same way they would with statically configured credentials."]])

(defn main [{:keys [connection-name conn-data embedded?]}]
  (r/with-let [status-sub (rf/subscribe [:federation/status])
               data-sub (rf/subscribe [:federation/data])
               form-sub (rf/subscribe [:federation/form])
               credentials-editing?-sub (rf/subscribe [:federation/credentials-editing?])
               mapping-editor-open?-sub (rf/subscribe [:federation/mapping-editor-open?])]
    (fn []
      (let [status @status-sub
            data @data-sub
            form @form-sub
            credentials-editing? @credentials-editing?-sub
            mapping-editor-open? @mapping-editor-open?-sub
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
            [:> FlaskConical {:size 14}]
            "Test as user"]]]

         [section
          "Admin role credentials"
          "The service account that the hook impersonates with. Must hold roles/iam.serviceAccountTokenCreator on the principals it issues credentials for."
          [sa-json-field form credentials-editing? has-credentials?]
          [forms/input
           {:label "GCP Project ID"
            :placeholder "e.g. my-gcp-project"
            :required true
            :not-margin-bottom? true
            :value (get-in form [:extra_config :project_id] "")
            :on-change #(rf/dispatch [:federation/set-nested-field
                                      :extra_config :project_id
                                      (-> % .-target .-value)])}]]

         [section
          "Identity mapping"
          "How the Hoop user's identity maps to the cloud IAM principal that gets impersonated."
          [identity-mapping-accordion form mapping-editor-open?]]

         [section
          "Output preview"
          "Environment variables the hook will write into the command runtime. These exist only for the duration of the session and are discarded on exit."
          [output-preview-section]])))))
