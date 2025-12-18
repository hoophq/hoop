(ns webapp.connections.views.setup.metadata-driven
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Card Flex Grid Heading Link Select Text]]
   ["lucide-react" :refer [FileSpreadsheet GlobeLock]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.config :as config]
   [webapp.connections.views.setup.additional-configuration :as additional-configuration]
   [webapp.connections.views.setup.agent-selector :as agent-selector]
   [webapp.connections.views.setup.headers :as headers]
    [webapp.connections.views.setup.page-wrapper :as page-wrapper]))

;; Credential normalization functions (also used in process_form.cljs)
(defn prefix-to-source
  "Convert a prefix string to its corresponding source string."
  [prefix]
  (cond
    (= prefix "_vaultkv1:") "vault-kv1"
    (= prefix "_vaultkv2:") "vault-kv2"
    (= prefix "_aws:") "aws-secrets-manager"
    (= prefix "_aws_iam_rds:") "aws-iam-role"
    :else nil))

(defn normalize-credential-value
  "Normalize credentials into {:value :source}. Supports inferring source from legacy prefixes."
  [value]
  (cond
    (map? value)
    (let [raw-value (if (contains? value :value) (:value value) value)
          source (or (:source value)
                     (when-let [prefix (:prefix value)]
                       (prefix-to-source prefix))
                     "manual-input")]
      {:value (str raw-value)
       :source source})

    (string? value)
    (let [prefixes ["_aws_iam_rds:" "_vaultkv1:" "_vaultkv2:" "_aws:"]
          matched-prefix (some (fn [prefix]
                                 (when (cs/starts-with? value prefix)
                                   prefix))
                               prefixes)
          stripped-value (if matched-prefix
                           (subs value (count matched-prefix))
                           value)
          source (or (prefix-to-source matched-prefix)
                     "manual-input")]
      {:value stripped-value
       :source source})

    :else {:value (str value)
           :source "manual-input"}))

(defn normalize-credentials
  "Normalize a map of credentials from prefixes/strings into {:value :source}."
  [credentials]
  (reduce-kv (fn [acc k v]
               (assoc acc k (normalize-credential-value v)))
             {}
             credentials))

(defn infer-connection-method
  "Infer connection method from credential sources"
  [credentials]
  (let [normalized (normalize-credentials credentials)
        sources (keep (fn [[_k v]]
                        (when (map? v) (:source v)))
                      normalized)]
    (cond
      (some #(= % "aws-iam-role") sources)
      {:connection-method "aws-iam-role"
       :secrets-manager-provider nil}

      (some #(or (= % "vault-kv1") (= % "vault-kv2")) sources)
      {:connection-method "secrets-manager"
       :secrets-manager-provider (if (some #(= % "vault-kv1") sources)
                                   "vault-kv1"
                                   "vault-kv2")}

      (some #(= % "aws-secrets-manager") sources)
      {:connection-method "secrets-manager"
       :secrets-manager-provider "aws-secrets-manager"}

      :else
      {:connection-method "manual-input"
       :secrets-manager-provider nil})))

;; Connection method selector components
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

(defn connection-method-selector
  "Connection method selector for connection setup (not role-based)"
  [connection-subtype]
  (let [connection-method @(rf/subscribe [:connection-setup/connection-method])
        supports-aws-iam? (contains? #{"mysql" "postgres"} connection-subtype)]
    [:> Box {:class "space-y-3"}
     [connection-method-card
      {:icon FileSpreadsheet
       :title "Manual Input"
       :description "Enter credentials directly, including host, user, password, and other connection details."
       :selected? (= connection-method "manual-input")
       :on-click #(rf/dispatch [:connection-setup/update-connection-method "manual-input"])}]
     [connection-method-card
      {:icon GlobeLock
       :title "Secrets Manager"
       :description "Connect to a secrets provider like AWS Secrets Manager or HashiCorp Vault to automatically fetch your resource credentials."
       :selected? (= connection-method "secrets-manager")
       :on-click #(rf/dispatch [:connection-setup/update-connection-method "secrets-manager"])}]
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
           :on-click #(rf/dispatch [:connection-setup/update-connection-method "aws-iam-role"])}]))]))

(defn secrets-manager-provider-selector []
  (let [provider @(rf/subscribe [:connection-setup/secrets-manager-provider])]
    [:> Box
     [forms/select {:label "Secrets manager provider"
                    :options [{:value "vault-kv1" :text "HashiCorp Vault KV version 1"}
                              {:value "vault-kv2" :text "HashiCorp Vault KV version 2"}
                              {:value "aws-secrets-manager" :text "AWS Secrets Manager"}]
                    :selected provider
                    :full-width? true
                    :not-margin-bottom? true
                    :on-change #(rf/dispatch [:connection-setup/update-secrets-manager-provider %])}]

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

(defn aws-iam-role-section []
  [:> Flex {:align "center" :gap "1" :mt "1"}
   [:> Text {:size "2" :class "text-[--gray-11]"}
    "Learn more about AWS IAM Role setup in"]
   [:> Link {:href (get-in config/docs-url [:setup :configuration :aws-iam-role])
             :target "_blank"
             :class "inline-flex items-center"}
    [:> Text {:size "2"}
     "our Docs ↗"]]])

(defn source-selector [field-key]
  (let [open? (r/atom false)
        source-text (fn [source]
                      (case source
                        "vault-kv1" "Vault KV v1"
                        "vault-kv2" "Vault KV v2"
                        "aws-secrets-manager" "AWS Secrets Manager"
                        "manual-input" "Manual"
                        "Vault KV 1"))]
    (fn []
      (let [field-source @(rf/subscribe [:connection-setup/field-source field-key])
            secrets-provider (or @(rf/subscribe [:connection-setup/secrets-manager-provider]) "vault-kv1")
            actual-source (or field-source secrets-provider "vault-kv1")
            available-sources [{:value secrets-provider :text (source-text secrets-provider)}
                               {:value "manual-input" :text "Manual"}]]
        [:> Select.Root {:value actual-source
                         :open @open?
                         :onOpenChange #(reset! open? %)
                         :onValueChange (fn [new-source]
                                          (reset! open? false)
                                          (when (and new-source (not (cs/blank? new-source)))
                                            (rf/dispatch [:connection-setup/update-field-source
                                                          field-key
                                                          new-source])))}
         [:> Select.Trigger {:variant "ghost"
                             :size "1"
                             :class "border-none shadow-none text-xsm font-medium text-[--gray-11]"
                             :placeholder (or (source-text actual-source) "Vault KV 1")}]
         [:> Select.Content
          (for [source available-sources]
            ^{:key (:value source)}
            [:> Select.Item {:value (:value source)} (:text source)])]]))))

(defn metadata-credential-field
  [{:keys [key label value required placeholder type description
           connection-method is-filesystem?]}]
  (let [show-source-selector? (= connection-method "secrets-manager")
        field-value (if (map? value) (:value value) (str value))
        handle-change (fn [e]
                        (let [new-value (-> e .-target .-value)]
                          (if is-filesystem?
                            (rf/dispatch [:connection-setup/update-config-file-by-key
                                          key
                                          new-value])
                            (rf/dispatch [:connection-setup/update-metadata-credentials
                                          key
                                          new-value]))))]
    (cond
      (= type "textarea")
      [forms/textarea {:label label
                       :placeholder (or placeholder (str "e.g. " key))
                       :value field-value
                       :required required
                       :helper-text description
                       :on-change handle-change}]

      show-source-selector?
      [forms/input-with-adornment {:label label
                                   :placeholder (or placeholder (str "e.g. " key))
                                   :value field-value
                                   :required required
                                   :type (or type "password")
                                   :helper-text description
                                   :on-change handle-change
                                   :show-password? true
                                   :start-adornment [source-selector key]}]
      :else
      [forms/input {:label label
                    :placeholder (or placeholder (str "e.g. " key))
                    :value field-value
                    :required required
                    :type (or type "password")
                    :helper-text description
                    :on-change handle-change}])))

(defn metadata-credential->form-field
  "Converte credential do metadata (agora array) para formato de formulário"
  [{:keys [name type required description placeholder]}]
  {:key name
   :env-var-name name
   :label (cs/join " " (cs/split name #"_"))
   :value ""
   :required required
   :placeholder (or placeholder description)
   :original-type type
   :type (case type
           "filesystem" "textarea"
           "textarea" "textarea"
           "password")
   :description description})

(defn get-metadata-credentials-config
  "Busca credentials do metadata para uma conexão específica por subtype"
  [connection-subtype]
  (let [connections-metadata @(rf/subscribe [:connections->metadata])]
    (when connections-metadata
      (let [connection (->> (:connections connections-metadata)
                            (filter #(= (get-in % [:resourceConfiguration :subtype]) connection-subtype))
                            first)
            credentials (get-in connection [:resourceConfiguration :credentials])]

        (when (seq credentials)
          (let [fields (->> credentials
                            (map metadata-credential->form-field)
                            vec)]
            fields))))))



(defn metadata-credentials [connection-subtype form-type]
  (let [configs (get-metadata-credentials-config connection-subtype)
        saved-credentials @(rf/subscribe [:connection-setup/metadata-credentials])
        raw-credentials (if (= form-type :update)
                          saved-credentials
                          @(rf/subscribe [:connection-setup/metadata-credentials]))
        full-credentials (get-in @(rf/subscribe [:connection-setup/form-data]) [:metadata-credentials] {})
        connection-method @(rf/subscribe [:connection-setup/connection-method])
        config-files @(rf/subscribe [:connection-setup/configuration-files])
        full-config-files (get-in @(rf/subscribe [:connection-setup/form-data]) [:credentials :configuration-files] [])
        config-files-map (into {} (map (fn [{:keys [key value]}]
                                         [key (if (map? value) (:value value) (str value))])
                                       config-files))
        full-config-files-map (into {} (map (fn [{:keys [key value]}]
                                              ;; Extract value from {:value :prefix} format, handling double-wrapped case
                                              [key (if (map? value)
                                                     (let [inner-value (:value value)]
                                                       (if (map? inner-value)
                                                         ;; Double-wrapped: extract the inner value
                                                         {:value (if (map? inner-value) (:value inner-value) (str inner-value))
                                                          :prefix ""}
                                                         ;; Single wrapped: use as-is
                                                         value))
                                                     ;; Plain value: wrap it
                                                     {:value (str value) :prefix ""})])
                                            full-config-files))]
    (if configs
      [:> Box {:class "space-y-5"}
       [:> Heading {:as "h3" :size "4" :weight "bold"}
        "Environment credentials"]
       [:> Grid {:columns "1" :gap "4"}
        (for [field configs
              :let [field-key (:key field)
                    env-var-name (:env-var-name field field-key)
                    is-filesystem? (= (:original-type field) "filesystem")
                    is-aws-iam-role? (= connection-method "aws-iam-role")
                    is-password? (= env-var-name "PASS")
                    should-hide? (and is-aws-iam-role? is-password?)]
              :when (not should-hide?)]
          ^{:key field-key}
          (if is-filesystem?
            [metadata-credential-field (assoc field
                                              :key field-key
                                              :value (get full-config-files-map field-key (get config-files-map field-key ""))
                                              :connection-method connection-method
                                              :is-filesystem? true)]
            (let [full-value (get full-credentials field-key)
                  credential-value (if (map? full-value)
                                     full-value
                                     (get raw-credentials field-key ""))]
              [metadata-credential-field (assoc field
                                                :key field-key
                                                :value credential-value
                                                :connection-method connection-method)])))]]
      nil)))

(defn credentials-step [connection-subtype form-type]
  [:form {:class "max-w-[600px]"
          :id "metadata-credentials-form"
          :on-submit (fn [e]
                       (.preventDefault e)
                       (rf/dispatch [:connection-setup/next-step :additional-config]))}
   [:> Box {:class "space-y-7"}
    (when connection-subtype
      [:<>
       [:> Box {:class "space-y-6"}
        [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12] mb-3"}
         "Connection method"]
        [connection-method-selector connection-subtype]

        (let [connection-method @(rf/subscribe [:connection-setup/connection-method])]
          (cond
            (= connection-method "secrets-manager")
            [secrets-manager-provider-selector]

            (= connection-method "aws-iam-role")
            [aws-iam-role-section]))]

       [metadata-credentials connection-subtype form-type]
       [agent-selector/main]])]])

(defn main [form-type]
  (let [connection-subtype @(rf/subscribe [:connection-setup/connection-subtype])
        current-step @(rf/subscribe [:connection-setup/current-step])
        agent-id @(rf/subscribe [:connection-setup/agent-id])]
    [page-wrapper/main
     {:children [:> Box {:class "max-w-[600px] mx-auto p-6 space-y-7"}
                 [headers/setup-header form-type]

                 (case current-step
                   :credentials [credentials-step connection-subtype form-type]
                   :additional-config [additional-configuration/main
                                       {:selected-type connection-subtype
                                        :form-type form-type
                                        :submit-fn #(rf/dispatch [:connection-setup/submit])}]
                   nil)]

      :footer-props {:form-type form-type
                     :next-text (if (= current-step :additional-config)
                                  "Confirm"
                                  "Next")
                     :on-click (fn []
                                 (let [form (.getElementById js/document
                                                             (if (= current-step :credentials)
                                                               "metadata-credentials-form"
                                                               "additional-config-form"))]
                                   (.reportValidity form)))
                     :next-disabled? (and (= current-step :credentials)
                                          (not agent-id))
                     :on-next (fn []
                                (let [form (.getElementById js/document
                                                            (if (= current-step :credentials)
                                                              "metadata-credentials-form"
                                                              "additional-config-form"))]
                                  (when form
                                    (let [is-valid (.reportValidity form)]
                                      (if (and is-valid agent-id)
                                        (let [event (js/Event. "submit" #js {:bubbles true :cancelable true})]
                                          (.dispatchEvent form event))
                                        (js/console.warn "Form validation failed or agent not selected!"))))))
                     :next-hidden? (= current-step :installation)
                     :hide-footer? (= current-step :installation)}}]))
