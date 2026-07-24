(ns webapp.shared-ui.sidebar.constants
  (:require
   ["@radix-ui/themes" :refer [Badge Flex Text]]
   ["lucide-react" :refer [BookMarked Boxes BrainCog CircleCheckBig GalleryVerticalEnd
                           Inbox LayoutDashboard PackageSearch Package Search
                           ShieldCheck SquareCode UserRoundCheck VenetianMask BookUp2
                           Sparkles KeyRound]]
   [re-frame.core :as rf]
   [webapp.config :as config]
   [webapp.routes :as routes]))

(def icons-registry
  {"Resources" (fn [& [{:keys [size] :or {size 24}}]]
                 [:> Package {:size size}])
   "Dashboard" (fn [& [{:keys [size] :or {size 24}}]]
                 [:> LayoutDashboard {:size size}])
   "Terminal" (fn [& [{:keys [size] :or {size 24}}]]
                [:> SquareCode {:size size}])
   "Runbooks" (fn [& [{:keys [size] :or {size 24}}]]
                [:> BookUp2 {:size size}])
   "Sessions" (fn [& [{:keys [size] :or {size 24}}]]
                [:> GalleryVerticalEnd {:size size}])
   "Reviews" (fn [& [{:keys [size] :or {size 24}}]]
               [:> Inbox {:size size}])
   "RunbooksSetup" (fn [& [{:keys [size] :or {size 24}}]]
                     [:> BookMarked {:size size}])
   "Guardrails" (fn [& [{:keys [size] :or {size 24}}]]
                  [:> ShieldCheck {:size size}])
   "JiraTemplates" (fn [& [{:keys [size] :or {size 24}}]]
                     (let [css-size (case size
                                      16 "w-4 h-4"
                                      24 "w-6 h-6"
                                      "w-6 h-6")]
                       [:img {:src (str config/webapp-url "/icons/icon-jira.svg")
                              :alt "Jira"
                              :class css-size}]))
   "AIDataMasking" (fn [& [{:keys [size] :or {size 24}}]]
                     [:> VenetianMask {:size size}])
   "AISessionAnalyzer" (fn [& [{:keys [size] :or {size 24}}]]
                         [:> Sparkles {:size size}])
   "AccessControl" (fn [& [{:keys [size] :or {size 24}}]]
                     [:> UserRoundCheck {:size size}])
   "AccessRequest" (fn [& [{:keys [size] :or {size 24}}]]
                     [:> CircleCheckBig {:size size}])
   "MachineIdentities" (fn [& [{:keys [size] :or {size 24}}]]
                         [:> KeyRound {:size size}])
   "ResourceDiscovery" (fn [& [{:keys [size] :or {size 24}}]]
                         [:> PackageSearch {:size size}])
   "Agents" (fn [& [{:keys [size] :or {size 24}}]]
              [:> BrainCog {:size size}])
   "authentication" (fn [& [{:keys [size] :or {size 24}}]]
                      [:> ShieldCheck {:size size}])
   "jira" (fn [& [{:keys [size] :or {size 24}}]]
            (let [css-size (case size
                             16 "w-4 h-4"
                             24 "w-6 h-6"
                             "w-6 h-6")]
              [:img {:src (str config/webapp-url "/icons/icon-jira.svg")
                     :class css-size}]))
   "webhooks" (fn [& [{:keys [size] :or {size 24}}]]
                [:> PackageSearch {:size size}])
   "slack" (fn [& [{:keys [size] :or {size 24}}]]
             [:> PackageSearch {:size size}])
   "infrastructure" (fn [& [{:keys [size] :or {size 24}}]]
                      [:> LayoutDashboard {:size size}])
   "license" (fn [& [{:keys [size] :or {size 24}}]]
               [:> ShieldCheck {:size size}])
   "users" (fn [& [{:keys [size] :or {size 24}}]]
             [:> UserRoundCheck {:size size}])
   "Search" (fn [& [{:keys [size] :or {size 24}}]]
              [:> Search {:size size}])
   "Provisioning" (fn [& [{:keys [size] :or {size 24}}]]
                    [:> Boxes {:size size}])})

;; Menu principal
(def main-routes
  [{:name "Resources"
    :label "Resources"
    :icon (get icons-registry "Resources")
    :uri (routes/url-for :resources)
    :navigate :resources
    :admin-only? false}
   {:name "Dashboard"
    :label "Dashboard"
    :icon (get icons-registry "Dashboard")
    :uri (routes/url-for :dashboard)
    :navigate :dashboard
    :admin-only? true}
   {:name "Terminal"
    :label "Terminal"
    :icon (get icons-registry "Terminal")
    :uri (routes/url-for :editor-plugin)
    :navigate :editor-plugin
    :admin-only? false}
   {:name "Runbooks"
    :label "Runbooks"
    :icon (get icons-registry "Runbooks")
    :uri (routes/url-for :runbooks)
    :navigate :runbooks
    :admin-only? false
    :license-feature "runbooks"}
   {:name "Sessions"
    :label "Sessions"
    :icon (get icons-registry "Sessions")
    :uri (routes/url-for :sessions)
    :navigate :sessions
    :admin-only? false}
   {:name "Provisioning"
    :label "Provisioning"
    :icon (get icons-registry "Provisioning")
    :uri (routes/url-for :provisioning)
    :navigate :provisioning
    :admin-only? true
    :license-feature "provisioning-hub"}
   {:name "Search"
    :label "Search"
    :icon (get icons-registry "Search")
    :action #(rf/dispatch [:command-palette->open])
    :admin-only? false
    :badge (fn []
             [:> Flex {:gap "3"}
              [:> Text {:weight "regular"} "cmd + K"]
              [:> Badge {:variant "solid" :color "green"}
               "NEW"]])}])

;; Seção Discover
(def discover-routes
  [{:name "AccessRequest"
    :label "Access Request"
    :icon (get icons-registry "AccessRequest")
    :uri (routes/url-for :access-request)
    :navigate :access-request
    :admin-only? true
    :license-feature "access-requests"}
   {:name "RunbooksSetup"
    :label "Runbooks Setup"
    :icon (get icons-registry "RunbooksSetup")
    :uri (routes/url-for :runbooks-setup)
    :navigate :runbooks-setup
    :admin-only? true
    :license-feature "runbooks"}
   {:name "Guardrails"
    :label "Guardrails"
    :icon (get icons-registry "Guardrails")
    :uri (routes/url-for :guardrails)
    :navigate :guardrails
    :admin-only? true
    :license-feature "guardrails"}
   {:name "JiraTemplates"
    :label "Jira Templates"
    :icon (get icons-registry "JiraTemplates")
    :uri (routes/url-for :jira-templates)
    :navigate :jira-templates
    :admin-only? true
    :license-feature "jira-integration"}
   {:name "AISessionAnalyzer"
    :label "AI Session Analyzer"
    :icon (get icons-registry "AISessionAnalyzer")
    :uri (routes/url-for :ai-session-analyzer)
    :navigate :ai-session-analyzer
    :admin-only? true
    :license-feature "ai-session-analyzer"}
   {:name "AIDataMasking"
    :label "Live Data Masking"
    :icon (get icons-registry "AIDataMasking")
    :uri (routes/url-for :ai-data-masking)
    :navigate :ai-data-masking
    :admin-only? true
    :license-feature "data-masking"}
   {:name "AccessControl"
    :label "Access Control"
    :icon (get icons-registry "AccessControl")
    :uri (routes/url-for :access-control)
    :navigate :access-control
    :admin-only? true
    :license-feature "access-control"}
   #_{:name "JustInTimeAccess"
      :label "Just-in-Time Access"
      :icon (fn []
              [:> AlarmClockCheck {:size 24}])
      :uri (routes/url-for :just-in-time)
      :navigate :just-in-time
      :admin-only? true}
   {:name "ResourceDiscovery"
    :label "Resource Discovery"
    :icon (get icons-registry "ResourceDiscovery")
    :uri (routes/url-for :integrations-aws-connect)
    :navigate :integrations-aws-connect
    :admin-only? true
    :license-feature "resource-discovery"
    :badge "BETA"}
   {:name "MachineIdentities"
    :label "Machine Identities"
    :icon (get icons-registry "MachineIdentities")
    :uri (routes/url-for :machine-identities)
    :navigate :machine-identities
    :admin-only? true
    :license-feature "machine-identities"}])

;; Seção Settings
(def organization-routes
  [{:name "Agents"
    :label "Agents"
    :icon (get icons-registry "Agents")
    :uri (routes/url-for :agents)
    :navigate :agents
    :admin-only? true}])

;; Integrations
(def integrations-management
  [{:name "authentication"
    :label "Authentication"
    :plugin? false
    :uri (routes/url-for :integrations-authentication)
    :navigate :integrations-authentication
    :admin-only? true
    :selfhosted-only? true}
   {:name "jira"
    :label "Jira"
    :plugin? false
    :uri (routes/url-for :settings-jira)
    :navigate :settings-jira
    :admin-only? true
    :selfhosted-only? false
    :license-feature "jira-integration"}
   {:name "webhooks"
    :label "Webhooks"
    :plugin? true
    :admin-only? true
    :selfhosted-only? false}
   {:name "slack"
    :label "Slack"
    :plugin? true
    :admin-only? true
    :selfhosted-only? false}])

;; Settings
(def settings-management
  [{:name "api-keys"
    :label "API Keys"
    :uri (routes/url-for :settings-api-keys)
    :navigate :settings-api-keys
    :admin-only? true
    :selfhosted-only? false
    :badge "NEW"}
   {:name "attributes"
    :label "Attributes"
    :uri (routes/url-for :settings-attributes)
    :navigate :settings-attributes
    :admin-only? true
    :selfhosted-only? false
    :badge "NEW"}
   {:name "infrastructure"
    :label "Infrastructure"
    :uri (routes/url-for :settings-infrastructure)
    :navigate :settings-infrastructure
    :admin-only? true
    :selfhosted-only? true}
   {:name "experimental"
    :label "Experimental"
    :uri (routes/url-for :settings-experimental)
    :navigate :settings-experimental
    :admin-only? true
    :selfhosted-only? false
    :badge "BETA"}
   {:name "audit-logs"
    :label "Internal Audit Logs"
    :uri (routes/url-for :settings-audit-logs)
    :navigate :settings-audit-logs
    :admin-only? true
    :selfhosted-only? false}
   {:name "license"
    :label "License"
    :uri (routes/url-for :license-management)
    :navigate :license-management
    :admin-only? true
    :selfhosted-only? false}
   {:name "users"
    :label "Users"
    :uri (routes/url-for :users)
    :navigate :users
    :admin-only? true
    :selfhosted-only? false}])
