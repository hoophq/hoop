(ns webapp.resources.setup.connection-method
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Card Flex Heading Link Select Text]]
   ["lucide-react" :refer [FileSpreadsheet GlobeLock]]
   [clojure.string :as str]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.config :as config]))

(defn source-selector [role-index field-key]
  (let [open? (r/atom false)
        source-text (fn [source]
                      (case source
                        "vault-kv1" "Vault KV v1"
                        "vault-kv2" "Vault KV v2"
                        "aws-secrets-manager" "AWS Secrets Manager"
                        "manual-input" "Manual"
                        "Vault KV 1"))
        connection-method-sub (rf/subscribe [:resource-setup/role-connection-method role-index])
        secrets-provider-sub (rf/subscribe [:resource-setup/secrets-manager-provider role-index])
        env-current-value-source-sub (rf/subscribe [:resource-setup/role-env-current-value-source role-index])
        field-source-sub (rf/subscribe [:resource-setup/field-source role-index field-key])]
    (fn []
      (let [is-env-var? (or (str/starts-with? field-key "env-var-")
                            (= field-key "env-current-value"))
            var-index (when (and is-env-var? (not= field-key "env-current-value"))
                        (js/parseInt (subs field-key 8)))
            env-var-source-sub (when var-index
                                 (rf/subscribe [:resource-setup/role-env-var-source role-index var-index]))
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
            actual-source (cond
                            (= connection-method "secrets-manager")
                            (or field-source secrets-provider)

                            :else
                            (or field-source "manual-input"))
            available-sources [{:value secrets-provider :text (source-text secrets-provider)}
                               {:value "manual-input" :text "Manual"}]]
        [:> Select.Root {:value actual-source
                         :open @open?
                         :onOpenChange #(reset! open? %)
                         :onValueChange (fn [new-source]
                                          (reset! open? false)
                                          (when (and new-source
                                                     (not (str/blank? new-source))
                                                     (not (empty? new-source))
                                                     (not= new-source actual-source))
                                            (cond
                                              (and is-env-var? (= field-key "env-current-value"))
                                              (rf/dispatch [:resource-setup->update-role-env-current-value-source role-index new-source])

                                              is-env-var?
                                              (rf/dispatch [:resource-setup->update-role-env-var-source role-index var-index new-source])

                                              :else
                                              (rf/dispatch [:resource-setup->update-field-source
                                                            role-index
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
  (let [connection-method-sub (rf/subscribe [:resource-setup/role-connection-method role-index])
        resource-subtype-sub (rf/subscribe [:resource-setup/resource-subtype])]
    (fn []
      (let [connection-method @connection-method-sub
            resource-subtype @resource-subtype-sub
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
                                        "aws-iam-role"])}]))]))))

(defn secrets-manager-provider-selector [role-index]
  (let [provider-sub (rf/subscribe [:resource-setup/secrets-manager-provider role-index])]
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
            "our Docs ↗"]]]]))))

(defn aws-iam-role-section [_role-index]
  [:> Flex {:align "center" :gap "1" :mt "1"}
   [:> Text {:size "2" :class "text-[--gray-11]"}
    "Learn more about AWS IAM Role setup in"]
   [:> Link {:href (get-in config/docs-url [:setup :configuration :rds-iam-auth])
             :target "_blank"
             :class "inline-flex items-center"}
    [:> Text {:size "2"}
     "our Docs ↗"]]])

(defn main
  [role-index]
  (let [connection-method-sub (rf/subscribe [:resource-setup/role-connection-method role-index])]
    (fn []
      (let [connection-method @connection-method-sub]
        [:> Box {:class "space-y-6"}
         [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12] mb-3"}
          "Role connection method"]
         [role-connection-method-selector role-index]

         (cond
           (= connection-method "secrets-manager")
           [secrets-manager-provider-selector role-index]

           (= connection-method "aws-iam-role")
           [aws-iam-role-section role-index])]))))

