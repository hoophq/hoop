(ns webapp.shared-ui.sidebar.constants
  (:require
   ["@radix-ui/themes" :refer [Badge Flex Text]]
   ["lucide-react" :refer [BookMarked Boxes GalleryVerticalEnd
                           Inbox LayoutDashboard PackageSearch Package Search
                           ShieldCheck SquareCode VenetianMask BookUp2
                           Sparkles KeyRound]]
   [re-frame.core :as rf]
   [webapp.config :as config]
   [webapp.routes :as routes]))

(def icons-registry
  {"Resources" (fn [& [{:keys [size] :or {size 24}}]]
                 [:> Package {:size size}])
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
   "MachineIdentities" (fn [& [{:keys [size] :or {size 24}}]]
                         [:> KeyRound {:size size}])
   "ResourceDiscovery" (fn [& [{:keys [size] :or {size 24}}]]
                         [:> PackageSearch {:size size}])
   "authentication" (fn [& [{:keys [size] :or {size 24}}]]
                      [:> ShieldCheck {:size size}])
   "jira" (fn [& [{:keys [size] :or {size 24}}]]
            (let [css-size (case size
                             16 "w-4 h-4"
                             24 "w-6 h-6"
                             "w-6 h-6")]
              [:img {:src (str config/webapp-url "/icons/icon-jira.svg")
                     :class css-size}]))
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
  [{:name "RunbooksSetup"
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
;; Emptied by EVL-116 (Track A1) ahead of the route deletions in A2/A3 —
;; /agents is served by the React shell and lives in its sidebar/palette.
;; The def itself is removed with its consumers in A3.
(def organization-routes
  [])

;; Integrations
(def integrations-management
  [{:name "authentication"
    :label "Authentication"
    :uri (routes/url-for :integrations-authentication)
    :navigate :integrations-authentication
    :admin-only? true
    :selfhosted-only? true}])

;; Settings
;; Emptied by EVL-116 (Track A1) ahead of the route deletions in A2/A3 — every
;; entry pointed at a /settings route the React shell already serves. The def
;; and its consumers (navigation.cljs, main.cljs, command_palette_constants.cljs)
;; are removed in A3.
(def settings-management
  [])
