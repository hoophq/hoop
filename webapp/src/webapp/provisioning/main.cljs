(ns webapp.provisioning.main
  (:require
   ["@radix-ui/themes" :refer [Box]]
   ["react" :as react]
   [re-frame.core :as rf]
   [webapp.provisioning.data :as data]
   [webapp.provisioning.events]
   [webapp.provisioning.subs]
   [webapp.provisioning.views.bulk-admin :as bulk-admin]
   [webapp.provisioning.views.bulk-import :as bulk-import]
   [webapp.provisioning.views.bulk-roles :as bulk-roles]
   [webapp.provisioning.views.inventory.main :as inventory]
   [webapp.provisioning.views.job-detail :as job-detail]
   [webapp.provisioning.views.session-list :as session-list]))

(defn- panel-inner []
  (let [;; this are global app state across pages
        resources @(rf/subscribe [:provisioning/resources])
        jobs      @(rf/subscribe [:provisioning/jobs])
        sessions  @(rf/subscribe [:provisioning/sessions])

        ;; local UI component state
        [selected-ids set-selected-ids]       (react/useState #{})
        [search set-search]                   (react/useState "")
        [active-tab set-active-tab]           (react/useState :inventory)
        [screen set-hub-screen]           (react/useState :hub)
        [bulk-import-open? set-bulk-import-open] (react/useState false)
        [dismissed-job-ids set-dismissed-job-ids] (react/useState #{})
        [hovered-row set-hovered-row]         (react/useState nil)
        [hub-page set-hub-page]               (react/useState 0)

        ;; using the useState react for transiant state in the commponents
        [bulk-resources set-bulk-resources]    (react/useState [])
        [bulk-configs set-bulk-configs]        (react/useState {})
        [bulk-admin-mode set-bulk-admin-mode]  (react/useState "manual")
        [bulk-roles-method set-bulk-roles-method] (react/useState "csv")
        [_viewing-job-id set-viewing-job-id]   (react/useState nil)
        [session-filter set-session-filter]    (react/useState {:title "Sessions"})
        [session-return-to set-session-return-to] (react/useState :hub)

        set-screen! (fn [s & [job-id]]
                      (set-hub-screen s)
                      (when job-id (set-viewing-job-id job-id)))

        open-bulk-admin! (fn [targets & [mode]]
                           (set-bulk-resources (vec targets))
                           (set-bulk-configs
                            (into {} (map (fn [r]
                                            [(:id r)
                                             (if (seq (:admin r))
                                               {:method   "manual"
                                                :username (:admin r)
                                                :password (or (:password r) "")}
                                               (data/make-default-config))])
                                          targets)))
                           (set-bulk-admin-mode (or mode "manual"))
                           (set-hub-screen :bulk-admin))

        open-bulk-roles! (fn [targets & [method]]
                           (set-bulk-resources (vec targets))
                           (set-bulk-roles-method (or method "csv"))
                           (set-hub-screen :bulk-roles))

        navigate-sessions!
        (fn [filter-map return-to]
          (set-session-filter filter-map)
          (set-session-return-to (or return-to :hub))
          (set-hub-screen :session-list))]

    ;; Fetch resources on mount
    (react/useEffect
     (fn []
       (rf/dispatch [:provisioning/fetch-resources])
       js/undefined)
     #js [])

    [:> Box {:class "flex flex-col bg-gray-1 px-10 pb-10 pt-10"
             :style {:height "100vh" :box-sizing "border-box" :overflow "hidden"}}

     ;; this controls the internal flow
     ;; most of the state is lifted up here in the parent component since it's shared across multiple child screens,
     ;; but some state is still kept in the child components for transient UI state (e.g. form inputs)

     (case screen
       :hub [inventory/view
             {:resources           resources
              :selected-ids        selected-ids
              :set-selected-ids    set-selected-ids
              :search              search
              :set-search          (fn [v] (set-search v) (set-hub-page 0))
              :active-tab          active-tab
              :set-active-tab      (fn [t] (set-active-tab t) (set-hub-page 0))
              :page                hub-page
              :set-page            set-hub-page
              :jobs                jobs
              :dismissed-job-ids   dismissed-job-ids
              :set-dismissed-job-ids set-dismissed-job-ids
              :hovered-row         hovered-row
              :set-hovered-row     set-hovered-row
              :on-set-screen       set-screen!
              :on-open-bulk-admin  open-bulk-admin!
              :on-open-bulk-roles  open-bulk-roles!
              :on-open-bulk-import #(set-bulk-import-open true)}]
       :bulk-admin [bulk-admin/bulk-admin-screen
                    {:resources    bulk-resources
                     :configs      bulk-configs
                     :set-configs  set-bulk-configs
                     :initial-mode bulk-admin-mode
                     :on-cancel    #(set-screen! :hub)
                     :on-done      (fn []
                                     (rf/dispatch [:provisioning/fetch-resources])
                                     (set-active-tab :provision)
                                     (set-screen! :hub))}]
       :bulk-roles [bulk-roles/bulk-roles-screen
                    {:resources      bulk-resources
                     :initial-method bulk-roles-method
                     :on-cancel      #(set-screen! :hub)
                     :on-apply       (fn [_method roles-by-resource]
                                       (rf/dispatch [:provisioning/start-role-plans
                                                     {:resources          bulk-resources
                                                      :roles-by-resource  (or roles-by-resource {})}])
                                       (set-hub-screen :job-detail))}]
      :job-detail [job-detail/job-detail-screen
                   {:on-back              #(set-screen! :hub)
                    :on-done              (fn []
                                            (rf/dispatch [:provisioning/fetch-resources])
                                            (set-screen! :hub))
                    :on-run-in-background #(set-screen! :hub)
                     :on-view-sessions     (fn [filter-opt]
                                             (let [plan-job @(rf/subscribe [:provisioning/plan-job])]
                                               (navigate-sessions!
                                                {:job-id      (:id plan-job)
                                                 :resource-id (:resource-id filter-opt)
                                                 :title       (if (:resource-name filter-opt)
                                                                (str "Sessions — " (:resource-name filter-opt))
                                                                "Sessions — Role provisioning")}
                                                :job-detail)))}]
       :session-list (let [filtered (filterv
                                     (fn [s]
                                       (and (or (nil? (:job-id session-filter))
                                                (= (:job-id session-filter) (:job-id s)))
                                            (or (nil? (:resource-id session-filter))
                                                (= (:resource-id session-filter) (:resource-id s)))))
                                     sessions)]
                       [session-list/session-list-screen
                        {:sessions  filtered
                         :title     (:title session-filter)
                         :subtitle  (str (count filtered)
                                         " session" (when (not= 1 (count filtered)) "s")
                                         " · each triggered by a provisioning step")
                         :on-back   #(set-screen! session-return-to)}])
 ;; do not render anything if the screen is unrecognized
       nil)

     (when bulk-import-open?
       [bulk-import/bulk-import-screen
        {:on-close  #(set-bulk-import-open false)
         :resources resources}])]))

(defn panel []
  [:f> panel-inner])
