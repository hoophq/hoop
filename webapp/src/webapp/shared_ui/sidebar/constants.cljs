(ns webapp.shared-ui.sidebar.constants
  (:require
   ["lucide-react" :refer [BadgeCheck BookMarked BrainCog GalleryVerticalEnd
                           Inbox LayoutDashboard PackageSearch Rotate3d
                           ShieldCheck Sparkles SquareCode UsersRound
                           UserRoundCheck VenetianMask]]
   [webapp.config :as config]
   [webapp.routes :as routes]))

;; Menu principal
(def main-routes
  [{:name "Connections"
    :label "Connections"
    :icon (fn []
            [:> Rotate3d {:size 24}])
    :uri (routes/url-for :connections)
    :navigate :connections
    :free-feature? true
    :admin-only? false}
   {:name "Dashboard"
    :label "Dashboard"
    :icon (fn []
            [:> LayoutDashboard {:size 24}])
    :uri (routes/url-for :dashboard)
    :navigate :dashboard
    :free-feature? false
    :upgrade-plan-route :upgrade-plan
    :admin-only? true}
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
   {:name "Reviews"
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
    :admin-only? true}
   {:name "Guardrails"
    :label "Guardrails"
    :icon (fn []
            [:> ShieldCheck {:size 24}])
    :uri (routes/url-for :guardrails)
    :navigate :guardrails
    :free-feature? true
    :admin-only? true}
   {:name "JiraTemplates"
    :label "Jira Templates"
    :icon (fn []
            [:div
             [:figure {:class "flex-shrink-0 w-6"}
              [:img {:src (str config/webapp-url "/icons/icon-jira.svg")}]]])
    :uri (routes/url-for :jira-templates)
    :navigate :jira-templates
    :free-feature? false
    :upgrade-plan-route :jira-templates
    :admin-only? true}
   {:name "AIQueryBuilder"
    :label "AI Query Builder"
    :icon (fn []
            [:> Sparkles {:size 24}])
    :uri (routes/url-for :manage-ask-ai)
    :navigate :manage-ask-ai
    :free-feature? false
    :upgrade-plan-route :upgrade-plan
    :admin-only? true}
   {:name "AIDataMasking"
    :label "AI Data Masking"
    :icon (fn []
            [:> VenetianMask {:size 24}])
    :uri (routes/url-for :ai-data-masking)
    :navigate :ai-data-masking
    :free-feature? false
    :upgrade-plan-route :upgrade-plan
    :admin-only? true}
   {:name "AccessControl"
    :label "Access Control"
    :icon (fn []
            [:> UserRoundCheck {:size 24}])
    :uri (routes/url-for :access-control)
    :navigate :access-control
    :free-feature? true
    :admin-only? true}
   #_{:name "JustInTimeAccess"
      :label "Just-in-Time Access"
      :icon (fn []
              [:> AlarmClockCheck {:size 24}])
      :uri (routes/url-for :just-in-time)
      :navigate :just-in-time
      :free-feature? false
      :upgrade-plan-route :upgrade-plan
      :admin-only? true}
   {:name "ResourceDiscovery"
    :label "Resource Discovery"
    :icon (fn []
            [:> PackageSearch {:size 24}])
    :uri (routes/url-for :integrations-aws-connect)
    :navigate :integrations-aws-connect
    :free-feature? false
    :upgrade-plan-route :upgrade-plan
    :admin-only? true
    :badge "BETA"}])

;; Seção Settings
(def organization-routes
  [{:name "Agents"
    :label "Agents"
    :icon (fn []
            [:> BrainCog {:size 24}])
    :uri (routes/url-for :agents)
    :navigate :agents
    :free-feature? true
    :admin-only? true}])

;; Integrations
(def integrations-management
  [{:name "authentication"
    :label "Authentication"
    :plugin? false
    :free-feature? true
    :uri (routes/url-for :integrations-authentication)
    :navigate :integrations-authentication
    :admin-only? true
    :selfhosted-only? true}
   {:name "jira"
    :label "Jira"
    :plugin? false
    :uri (routes/url-for :settings-jira)
    :navigate :settings-jira
    :free-feature? false
    :admin-only? true
    :selfhosted-only? false}
   {:name "webhooks"
    :label "Webhooks"
    :plugin? true
    :free-feature? false
    :admin-only? true
    :selfhosted-only? false}
   {:name "slack"
    :label "Slack"
    :plugin? true
    :free-feature? true
    :admin-only? true
    :selfhosted-only? false}])

;; Settings
(def settings-management
  [{:name "infrastructure"
    :label "Infrastructure"
    :uri (routes/url-for :settings-infrastructure)
    :navigate :settings-infrastructure
    :free-feature? true
    :admin-only? true
    :selfhosted-only? true}
   {:name "license"
    :label "License"
    :uri (routes/url-for :license-management)
    :navigate :license-management
    :free-feature? true
    :admin-only? true
    :selfhosted-only? false}
   {:name "users"
    :label "Users"
    :uri (routes/url-for :users)
    :navigate :users
    :free-feature? true
    :admin-only? true
    :selfhosted-only? false}])

;; Mantemos essa constante para compatibilidade com o código existente
(def routes main-routes)
