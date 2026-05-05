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
   [webapp.provisioning.views.hub :as hub]
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
        [hub-screen set-hub-screen]           (react/useState :hub)
        [bulk-import-open? set-bulk-import-open] (react/useState false)
        [dismissed-job-ids set-dismissed-job-ids] (react/useState #{})
        [hovered-row set-hovered-row]         (react/useState nil)

        ;; using the useState react for transiant state in the commponents
        [bulk-resources set-bulk-resources]    (react/useState [])
        [bulk-configs set-bulk-configs]        (react/useState {})
        [bulk-admin-mode set-bulk-admin-mode]  (react/useState "manual")
        [bulk-roles-method set-bulk-roles-method] (react/useState "create")
        [viewing-job-id set-viewing-job-id]    (react/useState nil)
        [session-filter set-session-filter]    (react/useState {:title "Sessions"})
        [session-return-to set-session-return-to] (react/useState :hub)

        set-screen! (fn [s & [job-id]]
                      (set-hub-screen s)
                      (when job-id (set-viewing-job-id job-id)))

        open-bulk-admin! (fn [targets & [mode]]
                           (set-bulk-resources (vec targets))
                           (set-bulk-configs
                            (into {} (map (fn [r] [(:id r) (data/make-default-config)]) targets)))
                           (set-bulk-admin-mode (or mode "manual"))
                           (set-hub-screen :bulk-admin))

        open-bulk-roles! (fn [targets & [method]]
                           (set-bulk-resources (vec targets))
                           (set-bulk-roles-method (or method "create"))
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

     ;; ── Hub ──
     (when (= hub-screen :hub)
       [hub/hub-view
        {:resources           resources
         :selected-ids        selected-ids
         :set-selected-ids    set-selected-ids
         :search              search
         :set-search          set-search
         :active-tab          active-tab
         :set-active-tab      set-active-tab
         :jobs                jobs
         :dismissed-job-ids   dismissed-job-ids
         :set-dismissed-job-ids set-dismissed-job-ids
         :hovered-row         hovered-row
         :set-hovered-row     set-hovered-row
         :on-set-screen       set-screen!
         :on-open-bulk-admin  open-bulk-admin!
         :on-open-bulk-roles  open-bulk-roles!
         :on-open-bulk-import #(set-bulk-import-open true)}])

     ;; ── Bulk Admin ──
     (when (= hub-screen :bulk-admin)
       [bulk-admin/bulk-admin-screen
        {:resources    bulk-resources
         :configs      bulk-configs
         :set-configs  set-bulk-configs
         :initial-mode bulk-admin-mode
         :on-cancel    #(set-screen! :hub)
         :on-apply     (fn [configs agent-id]
                         (let [job-id (data/start-job!
                                       {:type       :admin-setup
                                        :targets    bulk-resources
                                        :configs    configs
                                        :agent-id   agent-id})]
                           (set-viewing-job-id job-id)
                           (set-screen! :job-detail)))}])

     ;; ── Bulk Roles ──
     (when (= hub-screen :bulk-roles)
       [bulk-roles/bulk-roles-screen
        {:resources      bulk-resources
         :initial-method bulk-roles-method
         :on-cancel      #(set-screen! :hub)
         :on-apply       (fn [_method roles-by-resource]
                           (let [job-id (data/start-job!
                                         {:type              :role-provision
                                          :targets           bulk-resources
                                          :roles-by-resource roles-by-resource})]
                             (set-viewing-job-id job-id)
                             (set-screen! :job-detail)))}])

     ;; ── Bulk Import (modal overlay) ──
     (when bulk-import-open?
       [bulk-import/bulk-import-screen
        {:on-close   #(set-bulk-import-open false)
         :on-confirm (fn [new-resources]
                       (rf/dispatch [:provisioning/add-resources new-resources]))
         :resources  resources}])

     ;; ── Job Detail ──
     (when (= hub-screen :job-detail)
       (let [job (some #(when (= (:id %) viewing-job-id) %) jobs)]
         (when job
           [job-detail/job-detail-screen
            {:job                  job
             :sessions             sessions
             :on-back              #(set-screen! :hub)
             :on-run-in-background #(set-screen! :hub)
             :on-view-sessions     (fn [filter-opt]
                                     (navigate-sessions!
                                      {:job-id      (:id job)
                                       :resource-id (:resource-id filter-opt)
                                       :title       (if (:resource-name filter-opt)
                                                      (str "Sessions — " (:resource-name filter-opt))
                                                      (str "Sessions — " (:label job)))}
                                      :job-detail))}])))

     ;; ── Session List ──
     (when (= hub-screen :session-list)
       (let [filtered (filterv
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
           :on-back   #(set-screen! session-return-to)}]))]))

(defn panel []
  [:f> panel-inner])
