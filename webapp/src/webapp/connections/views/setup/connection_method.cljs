(ns webapp.connections.views.setup.connection-method
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading Link Select Text]]
   ["lucide-react" :refer [FileSpreadsheet GlobeLock]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.components.selection-card :refer [selection-card]]
   [webapp.config :as config]))

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
  "Normalize credentials into {:value :source}"
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

(defn connection-method-selector
  "Connection method selector for connection setup (not role-based)"
  [connection-subtype]
  (let [connection-method-sub (rf/subscribe [:connection-setup/connection-method])]
    (fn []
      (let [connection-method @connection-method-sub
            supports-aws-iam? (contains? #{"mysql" "postgres"} connection-subtype)]
        [:> Box {:class "space-y-3"}
         [selection-card
          {:icon (r/as-element [:> FileSpreadsheet {:size 20}])
           :title "Manual Input"
           :description "Enter credentials directly, including host, user, password, and other connection details."
           :selected? (= connection-method "manual-input")
           :on-click #(rf/dispatch [:connection-setup/update-connection-method "manual-input"])}]
         [selection-card
          {:icon (r/as-element [:> GlobeLock {:size 20}])
           :title "Secrets Manager"
           :description "Connect to a secrets provider like AWS Secrets Manager or HashiCorp Vault to automatically fetch your resource credentials."
           :selected? (= connection-method "secrets-manager")
           :on-click #(rf/dispatch [:connection-setup/update-connection-method "secrets-manager"])}]
         (when supports-aws-iam?
           (let [aws-icon (r/as-element
                           [:> Box {:class "w-5 h-5 flex items-center justify-center"}
                            [:img {:role "aws-icon"
                                   :src (str config/webapp-url "/icons/automatic-resources/aws.svg")
                                   :class "w-full h-full"
                                   :alt "AWS"}]])
                 icon-class (when (= connection-method "aws-iam-role") "brightness-0 invert")]
             [selection-card
              {:icon aws-icon
               :icon-class icon-class
               :title "AWS IAM Role"
               :description "Use an IAM Role that can be assumed to authenticate and access AWS resources."
               :selected? (= connection-method "aws-iam-role")
               :on-click #(rf/dispatch [:connection-setup/update-connection-method "aws-iam-role"])}]))]))))

(defn secrets-manager-provider-selector []
  (let [provider-sub (rf/subscribe [:connection-setup/secrets-manager-provider])]
    (fn []
      (let [provider @provider-sub]
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
            "our Docs ↗"]]]]))))

(defn aws-iam-role-section []
  [:> Flex {:align "center" :gap "1" :mt "1"}
   [:> Text {:size "2" :class "text-[--gray-11]"}
    "Learn more about AWS IAM Role setup in"]
   [:> Link {:href (get-in config/docs-url [:setup :configuration :rds-iam-auth])
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
                        "Vault KV 1"))
        connection-method-sub (rf/subscribe [:connection-setup/connection-method])
        secrets-provider-sub (rf/subscribe [:connection-setup/secrets-manager-provider])
        env-current-value-source-sub (rf/subscribe [:connection-setup/env-current-value-source])
        field-source-sub (rf/subscribe [:connection-setup/field-source field-key])]
    (fn []
      (let [is-env-var? (or (cs/starts-with? field-key "env-var-")
                            (= field-key "env-current-value"))
            var-index (when (and is-env-var? (not= field-key "env-current-value"))
                        (js/parseInt (subs field-key 8)))
            env-var-source-sub (when var-index
                                 (rf/subscribe [:connection-setup/env-var-source var-index]))
            connection-method @connection-method-sub
            secrets-provider (or @secrets-provider-sub "vault-kv1")
            env-current-value-source @env-current-value-source-sub
            field-source-value @field-source-sub
            field-source (cond
                           (and is-env-var? (= field-key "env-current-value"))
                           env-current-value-source

                           is-env-var?
                           @env-var-source-sub

                           :else
                           field-source-value)
            actual-source (or field-source
                              (when (= connection-method "secrets-manager") secrets-provider)
                              "manual-input")
            available-sources [{:value secrets-provider :text (source-text secrets-provider)}
                               {:value "manual-input" :text "Manual"}]]
        [:> Select.Root {:value actual-source
                         :open @open?
                         :onOpenChange #(reset! open? %)
                         :onValueChange (fn [new-source]
                                          (reset! open? false)
                                          (when (and new-source
                                                     (not (cs/blank? new-source))
                                                     (not (empty? new-source))
                                                     (not= new-source actual-source))
                                            (cond
                                              (and is-env-var? (= field-key "env-current-value"))
                                              (rf/dispatch [:connection-setup/update-env-current-value-source new-source])

                                              is-env-var?
                                              (rf/dispatch [:connection-setup/update-env-var-source var-index new-source])

                                              :else
                                              (rf/dispatch [:connection-setup/update-field-source
                                                            field-key
                                                            new-source]))))}
         [:> Select.Trigger {:variant "ghost"
                             :size "1"
                             :class "border-none shadow-none text-xsm font-medium text-[--gray-11]"
                             :placeholder (or (source-text actual-source) "Vault KV 1")}]
         [:> Select.Content
          (for [source available-sources]
            ^{:key (:value source)}
            [:> Select.Item {:value (:value source)} (:text source)])]]))))

(defn main
  [connection-subtype]
  (let [connection-method-sub (rf/subscribe [:connection-setup/connection-method])]
    (fn []
      (let [connection-method @connection-method-sub]
        [:> Box {:class "space-y-6"}
         [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12] mb-3"}
          "Connection method"]
         [connection-method-selector connection-subtype]

         (cond
           (= connection-method "secrets-manager")
           [secrets-manager-provider-selector]

           (= connection-method "aws-iam-role")
           [aws-iam-role-section])]))))

