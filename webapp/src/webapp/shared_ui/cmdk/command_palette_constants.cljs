(ns webapp.shared-ui.cmdk.command-palette-constants
  (:require
   ["lucide-react" :refer [SquareCode Settings]]
   [webapp.shared-ui.sidebar.constants :as sidebar-constants]
   [webapp.resources.helpers :refer [can-test-connection? can-connect? can-open-web-terminal?
                                     can-access-native-client?]]))

;; Simplified structure - direct pages + search only
(def main-navigation-items
  ;; Static pages based on sidebar menu
  (mapcat (fn [routes]
            (map (fn [route]
                   {:id (:name route)
                    :label (:label route)
                    :icon (fn [] [(get sidebar-constants/icons-registry (:name route)
                                       (fn [& _] [:> Settings {:size 16}])) {:size 16}])
                    :type :navigation
                    :action :navigate
                    :route (:navigate route)
                    :keywords [(:label route) (:name route)]})
                 routes))
          [sidebar-constants/main-routes
           sidebar-constants/discover-routes
           sidebar-constants/organization-routes
           sidebar-constants/integrations-management
           sidebar-constants/settings-management]))

;; Generate connection actions dynamically based on connection permissions
(defn get-connection-actions [connection admin? postgres-proxy-enabled?]
  (let [actions []]
    (cond-> actions
      ;; Web Terminal - only if can open web terminal
      (can-open-web-terminal? connection)
      (conj {:id "web-terminal"
             :label "Open in Web Terminal"
             :icon (fn [] [:> SquareCode {:size 16}])
             :action :web-terminal})

      ;; Local Terminal - only if can connect
      (can-connect? connection)
      (conj {:id "local-terminal"
             :label "Open with Hoop CLI"
             :icon (fn [] [:> SquareCode {:size 16}])
             :action :local-terminal})

      (can-access-native-client? connection postgres-proxy-enabled?)
      (conj {:id "open-native-client"
             :label "Open in Native Client"
             :icon (fn [] [:> SquareCode {:size 16}])
             :action :open-native-client})

      (can-test-connection? connection)
      (conj {:id "test-connection"
             :label "Test Connection"
             :icon (fn [] [:> SquareCode {:size 16}])
             :action :test-connection})

      ;; Configure - only for admins
      admin?
      (conj {:id "configure"
             :label "Configure"
             :icon (fn [] [:> Settings {:size 16}])
             :action :configure}))))

(defn filter-items-by-permissions [user-data]
  (let [admin? (:admin? user-data)
        selfhosted? (= (:tenancy_type user-data) "selfhosted")
        all-routes (concat sidebar-constants/main-routes
                           sidebar-constants/discover-routes
                           sidebar-constants/organization-routes
                           sidebar-constants/integrations-management
                           sidebar-constants/settings-management)]
    (->> main-navigation-items
         (filter (fn [item]
                   (let [route (first (filter #(= (:name %) (:id item)) all-routes))]
                     (and
                      (not= (:id item) "Search")
                      (or (not (:admin-only? route)) admin?)
                      (or (not (:selfhosted-only? route)) selfhosted?))))))))
