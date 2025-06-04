(ns webapp.shared-ui.sidebar.constants
  (:require ["@heroicons/react/24/outline" :as hero-outline-icon]
            [webapp.routes :as routes]))

;; Menu principal
(def main-routes
  [{:name "Dashboard"
    :label "Dashboard"
    :icon (fn [props]
            [:> hero-outline-icon/RectangleGroupIcon props])
    :uri (routes/url-for :dashboard)
    :navigate :dashboard
    :free-feature? false
    :admin-only? false}
   {:name "Connections"
    :label "Connections"
    :icon (fn [props]
            [:> hero-outline-icon/ArrowsRightLeftIcon props])
    :uri (routes/url-for :connections)
    :navigate :connections
    :free-feature? true
    :admin-only? false}
   {:name "Terminal"
    :label "Terminal"
    :icon (fn [props]
            [:> hero-outline-icon/CommandLineIcon props])
    :uri (routes/url-for :editor-plugin)
    :navigate :editor-plugin
    :free-feature? true
    :admin-only? false}
   {:name "Sessions"
    :label "Sessions"
    :icon (fn [props]
            [:> hero-outline-icon/RectangleStackIcon props])
    :uri (routes/url-for :sessions)
    :navigate :sessions
    :free-feature? true
    :admin-only? false}])

;; Plugins e extensões
(def plugins-routes
  [{:name "review"
    :label "Reviews"
    :icon (fn [props]
            [:> hero-outline-icon/InboxIcon props])
    :uri (routes/url-for :reviews-plugin)
    :free-feature? true
    :navigate :reviews-plugin
    :admin-only? false}])

;; Seção Discover
(def discover-routes
  [{:name "Runbooks"
    :label "Runbooks"
    :icon (fn [props]
            [:> hero-outline-icon/DocumentTextIcon props])
    :uri (routes/url-for :runbooks)
    :navigate :runbooks
    :free-feature? true
    :admin-only? false}
   {:name "Guardrails"
    :label "Guardrails"
    :icon (fn [props]
            [:> hero-outline-icon/ShieldCheckIcon props])
    :uri (routes/url-for :guardrails)
    :navigate :guardrails
    :free-feature? true
    :admin-only? false}
   {:name "JiraTemplates"
    :label "Jira Templates"
    :icon (fn [props]
            [:> hero-outline-icon/DocumentDuplicateIcon props])
    :uri (routes/url-for :jira-templates)
    :navigate :jira-templates
    :free-feature? false
    :admin-only? true}
   {:name "AIQueryBuilder"
    :label "AI Query Builder"
    :icon (fn [props]
            [:> hero-outline-icon/SparklesIcon props])
    :uri (routes/url-for :manage-ask-ai)
    :navigate :manage-ask-ai
    :free-feature? false
    :admin-only? false}
   {:name "AIDataMasking"
    :label "AI Data Masking"
    :icon (fn [props]
            [:> hero-outline-icon/EyeSlashIcon props])
    :uri (routes/url-for :ai-data-masking)
    :navigate :ai-data-masking
    :free-feature? false
    :admin-only? false}
   {:name "AccessControl"
    :label "Access Control"
    :icon (fn [props]
            [:> hero-outline-icon/FingerPrintIcon props])
    :uri (routes/url-for :access-control)
    :navigate :access-control
    :free-feature? false
    :admin-only? false}
   {:name "JustInTimeAccess"
    :label "Just-in-Time Access"
    :icon (fn [props]
            [:> hero-outline-icon/ClockIcon props])
    :uri (routes/url-for :just-in-time)
    :navigate :just-in-time
    :free-feature? false
    :admin-only? false}
   {:name "ResourceDiscovery"
    :label "Resource Discovery"
    :icon (fn [props]
            [:> hero-outline-icon/MagnifyingGlassIcon props])
    :uri (routes/url-for :resource-discovery)
    :navigate :resource-discovery
    :free-feature? false
    :admin-only? false
    :badge "BETA"}])

;; Seção Settings
(def settings-routes
  [{:name "Users"
    :label "Users"
    :icon (fn [props]
            [:> hero-outline-icon/UserGroupIcon props])
    :uri (routes/url-for :users)
    :navigate :users
    :free-feature? true
    :admin-only? true}
   {:name "Agents"
    :label "Agents"
    :icon (fn [props]
            [:> hero-outline-icon/ServerStackIcon props])
    :uri (routes/url-for :agents)
    :navigate :agents
    :free-feature? true
    :admin-only? true}
   {:name "License"
    :label "License"
    :icon (fn [props]
            [:> hero-outline-icon/KeyIcon props])
    :uri (routes/url-for :license-management)
    :navigate :license-management
    :free-feature? true
    :admin-only? true}])

;; Integrações (submenu de Settings)
(def integrations-management
  [{:name "jira"
    :label "Jira"
    :free-feature? false}
   {:name "webhooks"
    :label "Webhooks"
    :free-feature? false}
   {:name "slack"
    :label "Slack"
    :free-feature? true}])

;; Mantemos essa constante para compatibilidade com o código existente
(def routes main-routes)

;; Mantém os plugins-management para compatibilidade
(def plugins-management [{:name "runbooks"
                          :label "Runbooks"
                          :free-feature? true}])
