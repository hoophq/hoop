(ns webapp.shared-ui.sidebar.constants
  (:require
   ["@heroicons/react/24/outline" :as hero-outline-icon]
   ["lucide-react" :refer [LayoutDashboard Rotate3d SquareCode GalleryVerticalEnd
                           Inbox BookMarked ShieldCheck Sparkles VenetianMask
                           UserRoundCheck AlarmClockCheck PackageSearch UserRound
                           BrainCog Puzzle BadgeCheck]]
   [webapp.routes :as routes]))

;; Menu principal
(def main-routes
  [{:name "Dashboard"
    :label "Dashboard"
    :icon (fn []
            [:> LayoutDashboard {:size 24}])
    :uri (routes/url-for :dashboard)
    :navigate :dashboard
    :free-feature? false
    :admin-only? false}
   {:name "Connections"
    :label "Connections"
    :icon (fn []
            [:> Rotate3d {:size 24}])
    :uri (routes/url-for :connections)
    :navigate :connections
    :free-feature? true
    :admin-only? false}
   {:name "Terminal"
    :label "Terminal"
    :icon (fn []
            [:> SquareCode {:size 24}])
    :uri (routes/url-for :editor-plugin)
    :navigate :editor-plugin
    :free-feature? true
    :admin-only? false}
   {:name "Sessions"
    :label "Sessions"
    :icon (fn []
            [:> GalleryVerticalEnd {:size 24}])
    :uri (routes/url-for :sessions)
    :navigate :sessions
    :free-feature? true
    :admin-only? false}
   {:name "review"
    :label "Reviews"
    :icon (fn []
            [:> Inbox {:size 24}])
    :uri (routes/url-for :reviews-plugin)
    :free-feature? true
    :navigate :reviews-plugin
    :admin-only? false}])

;; Seção Discover
(def discover-routes
  [{:name "Runbooks"
    :label "Runbooks"
    :icon (fn []
            [:> BookMarked {:size 24}])
    :uri (routes/url-for :runbooks)
    :navigate :runbooks
    :free-feature? true
    :admin-only? false}
   {:name "Guardrails"
    :label "Guardrails"
    :icon (fn []
            [:> ShieldCheck {:size 24}])
    :uri (routes/url-for :guardrails)
    :navigate :guardrails
    :free-feature? true
    :admin-only? false}
   {:name "JiraTemplates"
    :label "Jira Templates"
    :icon (fn []
            [:> AlarmClockCheck {:size 24}])
    :uri (routes/url-for :jira-templates)
    :navigate :jira-templates
    :free-feature? false
    :admin-only? true}
   {:name "AIQueryBuilder"
    :label "AI Query Builder"
    :icon (fn []
            [:> Sparkles {:size 24}])
    :uri (routes/url-for :manage-ask-ai)
    :navigate :manage-ask-ai
    :free-feature? false
    :admin-only? false}
   #_{:name "AIDataMasking"
      :label "AI Data Masking"
      :icon (fn []
              [:> VenetianMask {:size 24}])
      :uri (routes/url-for :ai-data-masking)
      :navigate :ai-data-masking
      :free-feature? false
      :admin-only? false}
   {:name "AccessControl"
    :label "Access Control"
    :icon (fn []
            [:> UserRoundCheck {:size 24}])
    :uri (routes/url-for :access-control)
    :navigate :access-control
    :free-feature? false
    :admin-only? false}
   #_{:name "JustInTimeAccess"
      :label "Just-in-Time Access"
      :icon (fn []
              [:> AlarmClockCheck {:size 24}])
      :uri (routes/url-for :just-in-time)
      :navigate :just-in-time
      :free-feature? false
      :admin-only? false}
   {:name "ResourceDiscovery"
    :label "Resource Discovery"
    :icon (fn []
            [:> PackageSearch {:size 24}])
    :uri (routes/url-for :resource-discovery)
    :navigate :resource-discovery
    :free-feature? false
    :admin-only? false
    :badge "BETA"}])

;; Seção Settings
(def settings-routes
  [{:name "Users"
    :label "Users"
    :icon (fn []
            [:> UserRound {:size 24}])
    :uri (routes/url-for :users)
    :navigate :users
    :free-feature? true
    :admin-only? true}
   {:name "Agents"
    :label "Agents"
    :icon (fn []
            [:> BrainCog {:size 24}])
    :uri (routes/url-for :agents)
    :navigate :agents
    :free-feature? true
    :admin-only? true}
   {:name "License"
    :label "License"
    :icon (fn []
            [:> BadgeCheck {:size 24}])
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
