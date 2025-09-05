(ns webapp.shared-ui.cmdk.command-palette-constants
  (:require
   ["lucide-react" :refer [SquareCode Settings]]
   [webapp.shared-ui.sidebar.constants :as sidebar-constants]))

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

;; Helper functions to check connection permissions (same logic as connection-list)
(defn- can-connect? [connection]
  (= "enabled" (:access_mode_connect connection)))

(defn- can-open-web-terminal? [connection]
  (if-not (#{"tcp" "httpproxy" "ssh"} (:subtype connection))
    (or (= "enabled" (:access_mode_runbooks connection))
        (= "enabled" (:access_mode_exec connection)))
    false))

;; Generate connection actions dynamically based on connection permissions
(defn get-connection-actions [connection admin?]
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
             :label "Open in Local Terminal"
             :icon (fn [] [:> SquareCode {:size 16}])
             :action :local-terminal})

      ;; Configure - only for admins
      admin?
      (conj {:id "configure"
             :label "Configure"
             :icon (fn [] [:> Settings {:size 16}])
             :action :configure}))))

;; Filter and adjust items based on user permissions and license plan
(defn filter-items-by-permissions [user-data]
  (let [admin? (:admin? user-data)
        selfhosted? (= (:tenancy_type user-data) "selfhosted")
        free-license? (:free-license? user-data)
        ;; Include ALL routes for permission checking
        all-routes (concat sidebar-constants/main-routes
                           sidebar-constants/discover-routes
                           sidebar-constants/organization-routes
                           sidebar-constants/integrations-management
                           sidebar-constants/settings-management)]
    (->> main-navigation-items
         ;; Filter by basic permissions only (admin/selfhosted)
         (filter (fn [item]
                   (let [route (first (filter #(= (:name %) (:id item)) all-routes))]
                     (and
                      ;; Check admin-only
                      (or (not (:admin-only? route)) admin?)
                      ;; Check selfhosted-only
                      (or (not (:selfhosted-only? route)) selfhosted?)))))
         ;; Adjust routes for upgrade when needed (WITHOUT filtering)
         (map (fn [item]
                (let [route (first (filter #(= (:name %) (:id item)) all-routes))]
                  (if (and free-license? (not (:free-feature? route)))
                    ;; Paid feature on free license - redirect to upgrade
                    (assoc item
                           :action :navigate
                           :route (or (:upgrade-plan-route route) :upgrade-plan)
                           :requires-upgrade? true)
                    ;; Normal feature
                    item)))))))
