(ns webapp.shared-ui.sidebar.constants
  (:require
   ["@radix-ui/themes" :refer [Badge Flex Text]]
   ["lucide-react" :refer [BookMarked BrainCog GalleryVerticalEnd
                           Inbox LayoutDashboard PackageSearch Rotate3d Search
                           ShieldCheck SquareCode UserRoundCheck VenetianMask BookUp2]]
   [webapp.config :as config]
   [webapp.routes :as routes]))

;; Dicionário central de ícones com tamanho flexível (fonte única da verdade)
(def icons-registry
  {"Connections" (fn [& [{:keys [size] :or {size 24}}]]
                   [:> Rotate3d {:size size}])
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
                              :class css-size}]))
   "AIDataMasking" (fn [& [{:keys [size] :or {size 24}}]]
                     [:> VenetianMask {:size size}])
   "AccessControl" (fn [& [{:keys [size] :or {size 24}}]]
                     [:> UserRoundCheck {:size size}])
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
              [:> Search {:size size}])})

;; Menu principal
(def main-routes
  [{:name "Connections"
    :label "Connections"
    :icon (get icons-registry "Connections")
    :uri (routes/url-for :connections)
    :navigate :connections
    :free-feature? true
    :admin-only? false}
   {:name "Dashboard"
    :label "Dashboard"
    :icon (get icons-registry "Dashboard")
    :uri (routes/url-for :dashboard)
    :navigate :dashboard
    :free-feature? false
    :upgrade-plan-route :upgrade-plan
    :admin-only? true}
   {:name "Terminal"
    :label "Terminal"
    :icon (get icons-registry "Terminal")
    :uri (routes/url-for :editor-plugin)
    :navigate :editor-plugin
    :free-feature? true
    :admin-only? false}
   {:name "Runbooks"
    :label "Runbooks"
    :icon (get icons-registry "Runbooks")
    :uri (routes/url-for :runbooks)
    :navigate :runbooks
    :free-feature? true
    :admin-only? false}
   {:name "Sessions"
    :label "Sessions"
    :icon (get icons-registry "Sessions")
    :uri (routes/url-for :sessions)
    :navigate :sessions
    :free-feature? true
    :admin-only? false}
   {:name "Reviews"
    :label "Reviews"
    :icon (get icons-registry "Reviews")
    :uri (routes/url-for :reviews-plugin)
    :free-feature? true
    :navigate :reviews-plugin
    :admin-only? false}
   {:name "Search"
    :label "Search"
    :icon (get icons-registry "Search")
    :action #(rf/dispatch [:command-palette->open])
    :free-feature? true
    :admin-only? false
    :badge (fn []
             [:> Flex {:gap "3"}
              [:> Text {:weight "regular"} "cmd + k"]
              [:> Badge {:variant "solid" :color "green"}
               "NEW"]])}])

;; Seção Discover
(def discover-routes
  [{:name "RunbooksSetup"
    :label "Runbooks Setup"
    :icon (get icons-registry "RunbooksSetup")
    :uri (routes/url-for :runbooks-setup)
    :navigate :runbooks-setup
    :free-feature? true
    :admin-only? true}
   {:name "Guardrails"
    :label "Guardrails"
    :icon (get icons-registry "Guardrails")
    :uri (routes/url-for :guardrails)
    :navigate :guardrails
    :free-feature? true
    :admin-only? true}
   {:name "JiraTemplates"
    :label "Jira Templates"
    :icon (get icons-registry "JiraTemplates")
    :uri (routes/url-for :jira-templates)
    :navigate :jira-templates
    :free-feature? false
    :upgrade-plan-route :jira-templates
    :admin-only? true}
   {:name "AIDataMasking"
    :label "AI Data Masking"
    :icon (get icons-registry "AIDataMasking")
    :uri (routes/url-for :ai-data-masking)
    :navigate :ai-data-masking
    :free-feature? false
    :upgrade-plan-route :upgrade-plan
    :admin-only? true}
   {:name "AccessControl"
    :label "Access Control"
    :icon (get icons-registry "AccessControl")
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
    :icon (get icons-registry "ResourceDiscovery")
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
    :icon (get icons-registry "Agents")
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
