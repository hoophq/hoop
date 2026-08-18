(ns webapp.features.promotion
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Button Callout Flex Heading Link
                               Text]]
   ["lucide-react" :refer [ArrowUpRight Database FastForward FileLock2
                           Laptop ListCheck ListTodo Lock MonitorCheck
                           SearchCode Settings2 ShieldCheck
                           Sparkles UserRoundCheck]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.config :as config]))

(defn request-demo
  []
  (let [analytics-tracking @(rf/subscribe [:gateway->analytics-tracking])
        user (:data @(rf/subscribe [:users->current-user]))]
    (if analytics-tracking
      (do
        ;; Self-heal the messenger before opening it. The app-boot
        ;; :initialize-intercom runs when the user loads and reads
        ;; analytics_tracking from gateway->info; when the user resolves
        ;; first it shuts Intercom down and skips the boot, leaving
        ;; showNewMessage with a blank (unbooted) messenger window.
        (when-not (.-Intercom js/window)
          (rf/dispatch-sync [:tracking->load-scripts]))
        (when (and (.-Intercom js/window)
                   (not (.-booted js/window.Intercom)))
          (rf/dispatch-sync [:initialize-intercom user]))
        (if (.-Intercom js/window)
          (js/window.Intercom "showNewMessage" "I want to upgrade my current plan")
          (.open js/window "https://hoop.dev/meet" "_blank")))
      (.open js/window "https://hoop.dev/meet" "_blank"))))

(defn feature-item
  "Component to display a feature item with an icon"
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
  "Generic component to display feature state:
   - Empty state: when the feature is available but has no content
   - Upgrade plan: when the feature requires a plan upgrade

   Parameters:
   feature-name      - Name of the feature (Access Control, Guardrails, etc.)
   mode              - :empty-state or :upgrade-plan
   image             - Optional path under /images/illustrations/; omit for a placeholder panel
   description       - Short description of the feature
   feature-items     - List of items with feature details (title, description, icon)
   on-primary-click  - Function for the primary button click
   primary-text      - Text for the primary button (optional, default is based on mode)"
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

     [:> Box {:class "w-1/2 bg-blue-50 min-h-full flex-shrink-0"}
      (if image
        [:img {:src (str "/images/illustrations/" image)
               :alt (str feature-name " illustration")
               :class "w-full h-full object-cover"}]
        [:> Box {:class "w-full h-full min-h-[28rem]"}])]]))

;; The Guardrails, Access Control and Access Request promotions live in the
;; React app (webapp_v2 pages/Guardrails/components/GuardrailsPromotion.jsx,
;; pages/Features/AccessControl, pages/Features/AccessRequest) — the CLJS
;; components were removed with the CLJS pages that were their only consumers.

(defn runbooks-promotion
  "Specific component for Runbooks"
  [{:keys [mode on-promotion-seen]}]
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
                          (when on-promotion-seen
                            (on-promotion-seen))
                          (rf/dispatch [:navigate :runbooks-setup]))
                        request-demo)
    :primary-text (if (= mode :empty-state)
                    "Configure Runbooks"
                    "Request demo")}])

(defn parallel-mode-promotion
  "Specific component for Parallel Mode"
  [{:keys [mode]}]
  [feature-promotion
   {:feature-name "Parallel Mode"
    :mode mode
    :image "parallel-mode-promotion.png"
    :description "Improve your workflow and boost your productivity"
    :feature-items [{:icon [:> FastForward {:size 20}]
                     :title "Simplified execution flow"
                     :description "Run multiple resources simultaneously with a redesigned, faster, and more intuitive experience."}
                    {:icon [:> ListTodo {:size 20}]
                     :title "Smarter selection & control"
                     :description "Access from Terminal or Runbooks to easily search, filter, or select by resource type with instant prefill."}
                    {:icon [:> MonitorCheck {:size 20}]
                     :title "Clear visibility & feedback"
                     :description "Monitor with the new Execution Summary and identify statuses: Success, Error, or Approval Required  all in a single, organized view."}]
    :on-primary-click #(rf/dispatch [:parallel-mode/mark-promotion-seen])
    :primary-text "Get Started"}])

(defn users-promotion
  "Specific component for User Access"
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

(defn ai-session-analyzer-promotion
  "Specific component for AI Session Analyzer"
  [{:keys [mode on-promotion-seen]}]
  [feature-promotion
   {:feature-name "AI Session Analyzer"
    :mode mode
    :image "ai-session-analyzer-promotion.png"
    :description "Monitor terminal sessions and resource usage in real time."
    :feature-items [{:icon [:> SearchCode {:size 20}]
                     :title "Real-Time Risk Analysis"
                     :description "Analyze commands before execution to prevent security and reliability risks."}
                    {:icon [:> ShieldCheck {:size 20}]
                     :title "Configurable Rules"
                     :description "Admins define alert or block policies per resource."}
                    {:icon [:> Sparkles {:size 20}]
                     :title "Context-Aware AI Decisions"
                     :description "Use schema, indexes, and resource context to deliver accurate, trustworthy risk assessments."}]
    :on-primary-click (fn []
                        (when on-promotion-seen
                          (on-promotion-seen))
                        (rf/dispatch [:navigate :ai-session-analyzer]))
    :primary-text "Configure AI Session Analyzer"}])

(defn machine-identities-promotion
  "Specific component for Machine Identities"
  [{:keys [mode on-promotion-seen]}]
  [feature-promotion
   {:feature-name "Machine Identities"
    :mode mode
    :image "machine-identities-promotion.png"
    :description "Enable services and other non-human entities."
    :feature-items [{:icon [:> Laptop {:size 20}]
                     :title "Service Identity Management"
                     :description "Create secure identities for services and applications."}
                    {:icon [:> Database {:size 20}]
                     :title "Data Identification"
                     :description "Detect sensitive data flows between services and environments."}
                    {:icon [:> Lock {:size 20}]
                     :title "Access Control"
                     :description "Control how machine identities access infrastructure resources."}]
    :on-primary-click (fn []
                        (when on-promotion-seen
                          (on-promotion-seen))
                        (rf/dispatch [:navigate :machine-identities]))
    :primary-text "Configure Machine Identities"}])
