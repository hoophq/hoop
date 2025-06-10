(ns webapp.features.promotion
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Button Callout Flex Heading Link
                               Text]]
   ["lucide-react" :refer [Combine FileLock2 FolderLock ListCheck ListTodo
                           Settings2 ShieldCheck SlidersHorizontal TextSearch
                           UserRoundCheck ArrowUpRight]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.config :as config]))

(defn feature-item
  "Componente para exibir um item da feature com ícone"
  [{:keys [icon title description]}]
  [:> Flex {:align "start" :gap "5"}
   [:> Avatar {:size "4"
               :variant "soft"
               :fallback  (r/as-element
                           icon)}]
   [:> Flex {:direction "column" :gap "1"}
    [:> Heading {:size "5" :weight "bold" :class "text-gray-12"}
     title]
    [:> Text {:size "3" :class "text-gray-12"}
     description]]])

(defn feature-promotion
  "Componente genérico para exibir estado de feature:
   - Empty state: quando a feature está disponível mas não tem conteúdo
   - Upgrade plan: quando a feature requer upgrade de plano

   Parâmetros:
   feature-name      - Nome da feature (Access Control, Guardrails, etc.)
   mode              - :empty-state ou :upgrade-plan
   image             - Caminho da imagem (relativo a /images/illustrations/)
   description       - Descrição curta da feature
   feature-items     - Lista de itens com detalhes da feature (título, descrição, ícone)
   on-primary-click  - Função para o clique do botão principal
   primary-text      - Texto do botão principal (opcional, padrão baseado no mode)"
  [{:keys [feature-name
           mode
           image
           description
           feature-items
           on-primary-click
           primary-text
           extra-information
           link-button-href
           link-button-text]}]
  (let [is-empty-state? (= mode :empty-state)
        button-text (or primary-text
                        (if is-empty-state?
                          (str "Create new " feature-name)
                          "Request demo"))]
    [:> Box {:class "flex h-full overflow-hidden"}
     [:> Box {:class "w-1/2 p-12 space-y-radix-8 flex flex-col justify-center"}
      [:> Box
       [:> Heading {:size "8" :weight "bold" :class "text-gray-12"}
        (str "Get more with " feature-name)]

       [:> Text {:as "p" :size "5" :class "text-gray-11"}
        description]]

      [:> Box {:class "space-y-radix-6"}
       (for [item feature-items]
         ^{:key (:title item)}
         [feature-item item])]

      (when extra-information
        [:> Text {:size "2" :class "text-gray-11"}
         extra-information])

      (when (and link-button-href link-button-text)
        [:> Link {:href (get-in config/docs-url link-button-href)
                  :target "_blank"}
         [:> Callout.Root {:size "1" :variant "outline" :color "gray" :class "w-fit"}
          [:> Callout.Icon
           [:> ArrowUpRight {:size 16}]]
          [:> Callout.Text
           link-button-text]]])

      (when (and on-primary-click button-text)
        [:> Button {:size "3"
                    :onClick on-primary-click
                    :class "self-start"}
         button-text])]

     [:> Box {:class "w-1/2 bg-blue-50"}
      [:img {:src (str "/images/illustrations/" image)
             :alt (str feature-name " illustration")
             :class "w-full h-full object-cover"}]]]))

(defn access-control-promotion
  "Componente específico para Access Control"
  [{:keys [mode installed?]}]
  [feature-promotion
   {:feature-name "Access Control"
    :mode mode
    :image "access-control-promotion.png"
    :description "Transform your data management from unstructured to controlled with powerful permission rules."
    :feature-items [{:icon [:> ListCheck {:size 20}]
                     :title "Role-Based Access Control (RBAC)"
                     :description "Granular permission management for resources with flexible role assignments and group management."}
                    {:icon [:> Settings2 {:size 20}]
                     :title "Connection-Level Permissions"
                     :description "Group-based access management with customizable access levels per connection."}
                    {:icon [:> UserRoundCheck {:size 20}]
                     :title "Dynamic Access Management"
                     :description "Real-time access updates and modifications with seamless integration with identity providers."}]
    :on-primary-click (if (= mode :empty-state)
                        (if installed?
                          #(rf/dispatch [:navigate :access-control-new])
                          #(rf/dispatch
                            [:dialog->open
                             {:title "Activate Access Control"
                              :text "By activating this feature users will have their accesses blocked until a connection permission is set."
                              :text-action-button "Confirm"
                              :action-button? true
                              :type :info
                              :on-success (fn []
                                            (rf/dispatch [:plugins->create-plugin {:name "access_control"
                                                                                   :connections []}])
                                            (js/setTimeout
                                             (fn [] (rf/dispatch [:plugins->get-plugin-by-name "access_control"]))
                                             1000))}]))
                        #(js/window.Intercom
                          "showNewMessage"
                          "I want to upgrade my current plan"))
    :primary-text (if (= mode :empty-state)
                    "Activate Access Control"
                    "Request demo")}])

(defn guardrails-promotion
  "Componente específico para Guardrails"
  [{:keys [mode installed?]}]
  [feature-promotion
   {:feature-name "Guardrails"
    :mode mode
    :image "guardrails-promotion.png"
    :description "Create custom rules to guide and protect usage within your connections."
    :feature-items [{:icon [:> ListCheck {:size 20}]
                     :title "Automated Policy Enforcement"
                     :description "Real-time monitoring of access policies, automatic detection and prevention of risky operations with customizable rules based on your organization's security requirements."}
                    {:icon [:> ShieldCheck {:size 20}]
                     :title "Smart Command Filtering"
                     :description "Block potentially dangerous commands before execution and prevent accidental data modifications or deletions."}
                    {:icon [:> TextSearch {:size 20}]
                     :title "Context-Aware Access"
                     :description "Evaluate access requests based on user context, consider factors like time, location, and previous activity and create an adaptive security measurement based on risk assessment."}]
    :on-primary-click (if (= mode :empty-state)
                        #(rf/dispatch [:navigate :create-guardrail])
                        #(js/window.Intercom
                          "showNewMessage"
                          "I want to upgrade my current plan"))
    :primary-text (if (= mode :empty-state)
                    "Create new Guardrails"
                    "Request demo")}])

(defn jira-templates-promotion
  "Componente específico para Jira templates"
  [{:keys [mode installed?]}]
  [feature-promotion
   {:feature-name "JIRA Templates"
    :mode mode
    :image "jira-pomotion.png"
    :description "Automate change management and security workflows."
    :feature-items [{:icon [:> ListCheck {:size 20}]
                     :title "Automated Change Management"
                     :description "Reduce manual documentation and administrative overhead by automatically creating and tracking Jira tickets for every infrastructure access request."}
                    {:icon [:> Settings2 {:size 20}]
                     :title "Seamless Workflow Integration"
                     :description "Link access requests directly to Jira projects and request types with contextual information."}
                    {:icon [:> FileLock2 {:size 20}]
                     :title "Flexible User Prompts & Data Collection"
                     :description "Request additional information from users during access workflows. Map manual or automated data to Jira fields."}]
    :on-primary-click (if (= mode :empty-state)
                        #(rf/dispatch [:navigate :manage-plugin {} :plugin-name :jira])
                        #(js/window.Intercom
                          "showNewMessage"
                          "I want to upgrade my current plan"))
    :primary-text (if (= mode :empty-state)
                    "Configure Jira Integration"
                    "Request demo")}])

(defn runbooks-promotion
  "Componente específico para Runbooks"
  [{:keys [mode installed?]}]
  [feature-promotion
   {:feature-name "Runbooks"
    :mode mode
    :image "runbooks-promotion.png"
    :description "Automate operational tasks with version-controlled templates."
    :feature-items [{:icon [:> ListCheck {:size 20}]
                     :title "Fully Automated Tasks"
                     :description "Standardize operational procedures with interactive runbooks that guide users through complex tasks."}
                    {:icon [:> Settings2 {:size 20}]
                     :title "Complete Control"
                     :description "Create step-by-step procedures for common tasks and troubleshooting scenarios."}
                    {:icon [:> ShieldCheck {:size 20}]
                     :title "Flexibility with High-Level Security"
                     :description "Maintain security while allowing teams to execute approved operations efficiently."}]
    :on-primary-click (if (= mode :empty-state)
                        (fn []
                          (.setItem (.-localStorage js/window) "runbooks-promotion-seen" "true")
                          (rf/dispatch [:navigate :runbooks {:tab "configuration"}])
                          (rf/dispatch [:plugins->get-plugin-by-name "runbooks"]))
                        #(js/window.Intercom
                          "showNewMessage"
                          "I want to upgrade my current plan"))
    :primary-text (if (= mode :empty-state)
                    "Configure Runbooks"
                    "Request demo")}])

(defn users-promotion
  "Componente específico para User Access"
  [{:keys [mode]}]
  [feature-promotion
   {:feature-name "User Access"
    :mode mode
    :image "user-manage-promotion.png"
    :description "Set up team-based permissions and approval workflows for secure resource access."
    :feature-items [{:icon [:> ShieldCheck {:size 20}]
                     :title "Identity Providers Integration"
                     :description "Connect your existing identity solution (like Auth0, Okta, Google, Azure and more) to sync users and groups automatically."}
                    {:icon [:> UserRoundCheck {:size 20}]
                     :title "Access Control"
                     :description "Define precise boundaries around your infrastructure with flexible rules that protect sensitive resources and scale effortlessly."}
                    {:icon [:> ListTodo {:size 20}]
                     :title "Approval Workflows"
                     :description "Add intelligent security gates with real-time command reviews and just-in-time approvals."}]
    :on-primary-click #(rf/dispatch [:users/mark-promotion-seen])
    :primary-text "Get Started"}])

(defn ai-data-masking-promotion
  "Componente específico para AI Data Masking"
  [{:keys [mode redact-provider]}]
  [feature-promotion
   (merge
    {:feature-name "AI Data Masking"
     :mode mode
     :image "data-masking-promotion.png"
     :description "Zero-config DLP policies that automatically mask sensitive data in real-time at the protocol layer."
     :feature-items [{:icon [:> FolderLock {:size 20}]
                      :title "No Configuration Required"
                      :description "Automatically masks sensitive data in the data stream of any connection where AI Data Masking is enabled."}
                     {:icon [:> Combine {:size 20}]
                      :title "Real-Time Protection"
                      :description "Sensitive data is masked in real-time, ensuring that no unprotected data is exposed during access sessions."}
                     {:icon [:> SlidersHorizontal {:size 20}]
                      :title "Customizable Setup"
                      :description "Easily add or remove fields to tailor the masking setup to your specific needs."}]}
    (case redact-provider
      "mspresidio"
      {:on-primary-click (if (= mode :empty-state)
                           #(rf/dispatch [:navigate :create-ai-data-masking])
                           #(js/window.Intercom
                             "showNewMessage"
                             "I want to upgrade my current plan"))
       :primary-text (if (= mode :empty-state)
                       "Configure AI Data Masking"
                       "Request demo")}
      "gcp"
      {:link-button-href [:features :ai-datamasking]
       :link-button-text "Go to AI Data Masking Docs"
       :extra-information "Your organization has a deprecated Google Cloud DLP configuration. Check our Microsoft Presidio documentation to enable an upgraded version of AI Data Masking setup in your environment."}
      {:link-button-href [:features :ai-datamasking]
       :link-button-text "Go to AI Data Masking Docs"
       :extra-information "Your organization has a deprecated Google Cloud DLP configuration. Check our Microsoft Presidio documentation to enable an upgraded version of AI Data Masking setup in your environment."}))])
