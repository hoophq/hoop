(ns webapp.connections.views.resource-catalog.helpers
  (:require [clojure.string :as cs]
            [re-frame.core :as rf]))

;; Denylist - connections that will NOT appear in the catalog
(def denied-connections #{})

;; Custom connections that are not in metadata.json
(def custom-connections
  [{:id "linux-vm"
    :name "Linux VM or Container"
    :description "Connect to Linux virtual machines, Docker containers, or any remote server via SSH."
    :category "infrastructure-access"
    :icon-name "ssh"
    :tags ["linux" "vm" "container" "ssh" "infrastructure"]
    :overview {:description "Connect to any Linux-based system including virtual machines, Docker containers, bare metal servers, and cloud instances."
               :features ["Secure SSH-based access"
                          "Terminal session recording"
                          "Multi-user access control"
                          "Session sharing and collaboration"]
               :useCases ["Development environment access"
                          "Production server administration"
                          "Container debugging and management"
                          "Infrastructure maintenance and monitoring"]}
    :setupGuide {:accessMethods {:webapp true :cli true :runbooks true}
                 :requirements ["SSH server running on target system"
                                "Valid SSH credentials (password or key-based)"
                                "Network connectivity to port 22"
                                "Proper firewall configuration"]}
    :resourceConfiguration {:type "server"
                            :subtype "custom"}}])

;; Connections only for onboarding (execute direct actions)
(def onboarding-connections
  [{:id "postgres-demo"
    :name "Demo PostgresSQL"
    :description "Access a preloaded database to see it in action."
    :category "quickstart"
    :icon-name "postgres"
    :tags ["demo" "quickstart" "postgresql"]
    :action #(rf/dispatch [:connections->quickstart-create-postgres-demo])
    :special-type :action}
   {:id "aws-discovery"
    :name "Automatic resource discovery"
    :description "Access your resources through your infrastructure providers."
    :category "quickstart"
    :icon-name "aws"
    :tags ["aws" "discovery" "automatic" "beta"]
    :action #(rf/dispatch [:navigate :onboarding-resource-providers])
    :special-type :action}])

(def popular-connections #{"postgres" "mysql" "mongodb" "ssh" "linux-vm"
                           "postgres-demo"})

(defn is-onboarding-context?
  "Check if we're currently in onboarding context by URL"
  []
  (cs/includes? (.. js/window -location -pathname) "/onboarding"))

(defn compose-connections
  "Compose all connections: metadata + custom + specials (if onboarding)"
  [metadata-connections is-onboarding?]
  (let [filtered-metadata-connections (->> metadata-connections
                                           (remove #(denied-connections (:id %))))]
    (concat filtered-metadata-connections
            custom-connections
            (when is-onboarding? onboarding-connections))))

(defn has-active-filters?
  "Check if any filters are currently active"
  [{:keys [search-term selected-categories selected-tags]}]
  (or (not (cs/blank? search-term))
      (not-empty selected-categories)
      (not-empty selected-tags)))

(defn apply-filters
  "Apply all filters to connections list"
  [connections {:keys [search-term selected-categories selected-tags]}]
  (->> connections
       (filter (fn [conn]
                 (and
                  ;; Search filter
                  (if (cs/blank? search-term)
                    true
                    (or (cs/includes? (cs/lower-case (:name conn))
                                      (cs/lower-case search-term))
                        (cs/includes? (cs/lower-case (or (:description conn) ""))
                                      (cs/lower-case search-term))
                        (some #(cs/includes? (cs/lower-case %)
                                             (cs/lower-case search-term))
                              (:tags conn))))
                  ;; Category filter
                  (if (empty? selected-categories)
                    true
                    (contains? selected-categories (:category conn)))
                  ;; Tags filter
                  (if (empty? selected-tags)
                    true
                    (some #(contains? selected-tags %) (:tags conn))))))))

(defn get-popular-connections
  "Get popular connections based on context and filters"
  [connections is-onboarding? has-filters?]
  (when-not has-filters?
    (let [base-popular-connections (->> connections
                                        (filter #(popular-connections (:id %)))
                                        (take 5))]
      (if is-onboarding?
        (concat onboarding-connections
                (take 3 base-popular-connections))
        base-popular-connections))))

(defn extract-metadata
  "Extract categories and tags from connections"
  [connections]
  (let [all-categories (->> connections
                            (map :category)
                            (remove nil?)
                            distinct
                            sort)
        all-tags (->> connections
                      (mapcat :tags)
                      (remove nil?)
                      distinct
                      (take 20)
                      sort)]
    {:categories all-categories
     :tags all-tags}))

(def new-connections #{"postgres-demo"})
(def beta-connections #{"mongodb" "aws-discovery"})

;; Connection to setup flow mapping
(def connection-setup-mappings
  {;; Database connections (new resources flow)
   "postgres" {:type "database" :subtype "postgres"}
   "mysql" {:type "database" :subtype "mysql"}
   "mongodb" {:type "database" :subtype "mongodb"}
   "mssql" {:type "database" :subtype "mssql"}
   "oracle" {:type "database" :subtype "oracledb"}
   ;; Application connections (new resources flow)
   "ssh" {:type "application" :subtype "ssh"}
   "tcp" {:type "application" :subtype "tcp"}
   "httpproxy" {:type "application" :subtype "httpproxy"}
   ;; Custom connections (new resources flow)
   "linux-vm" {:type "custom" :subtype "linux-vm" :command ["bash"]}})

(defn get-connection-badge
  "Get badge info for a connection (NEW, BETA, etc)"
  [connection-id]
  (cond
    (new-connections connection-id) {:text "NEW" :color "green"}
    (beta-connections connection-id) {:text "BETA" :color "indigo"}
    :else nil))

(defn get-setup-config
  "Get setup configuration for a connection"
  [connection]
  (let [connection-id (:id connection)
        resource-config (:resourceConfiguration connection)]
    (if-let [mapped-config (connection-setup-mappings connection-id)]
      mapped-config
      {:type (:type resource-config)
       :subtype (:subtype resource-config)
       :command (:command resource-config)})))

(defn execute-special-action
  "Execute special action for connections with direct actions"
  [connection]
  (when (= (:special-type connection) :action)
    ((:action connection))))

(defn dispatch-setup-navigation
  "Dispatch navigation to appropriate setup flow"
  [setup-config is-onboarding?]
  ;; Initialize resource setup with data from catalog
  (rf/dispatch [:resource-setup->initialize-from-catalog setup-config])

  ;; Navigate to resource setup flow
  (if is-onboarding?
    (rf/dispatch [:navigate :onboarding-setup-resource])
    (rf/dispatch [:navigate :resource-setup-new])))
