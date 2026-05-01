(ns webapp.provisioning.main
  (:require
   ["@radix-ui/themes" :refer [Box]]
   [reagent.core :as r]
   [webapp.provisioning.data :as data]
   [webapp.provisioning.views.hub :as hub]
   [webapp.provisioning.views.bulk-admin :as bulk-admin]
   [webapp.provisioning.views.bulk-roles :as bulk-roles]
   [webapp.provisioning.views.bulk-import :as bulk-import]
   [webapp.provisioning.views.job-detail :as job-detail]
   [webapp.provisioning.views.session-list :as session-list]))

(defn panel []
  (let [;; Top-level state atoms
        resources-atom      (r/atom (vec data/initial-resources))
        selected-ids-atom   (r/atom #{})
        search-atom         (r/atom "")
        active-tab-atom     (r/atom :inventory)
        hub-screen-atom     (r/atom :hub)       ;; :hub | :bulk-admin | :bulk-roles | :bulk-import | :job-detail | :session-list
        jobs-atom           (r/atom [])
        dismissed-job-ids   (r/atom #{})
        sessions-atom       (r/atom [])
        hovered-row-atom    (r/atom nil)

        ;; Transient state for sub-screens
        bulk-resources-atom (r/atom [])
        bulk-configs-atom   (r/atom {})
        bulk-admin-mode     (r/atom "manual")
        bulk-roles-method   (r/atom "create")
        viewing-job-id      (r/atom nil)
        session-filter-atom (r/atom {:title "Sessions"})
        session-return-to   (r/atom :hub)]
    (fn []
      (let [screen @hub-screen-atom

            set-screen!
            (fn [s & [job-id]]
              (reset! hub-screen-atom s)
              (when job-id (reset! viewing-job-id job-id)))

            open-bulk-admin!
            (fn [targets & [mode]]
              (reset! bulk-resources-atom (vec targets))
              (reset! bulk-configs-atom
                      (into {} (map (fn [r] [(:id r) (data/make-default-config)]) targets)))
              (reset! bulk-admin-mode (or mode "manual"))
              (reset! hub-screen-atom :bulk-admin))

            open-bulk-roles!
            (fn [targets & [method]]
              (reset! bulk-resources-atom (vec targets))
              (reset! bulk-roles-method (or method "create"))
              (reset! hub-screen-atom :bulk-roles))

            navigate-sessions!
            (fn [filter-map return-to]
              (reset! session-filter-atom filter-map)
              (reset! session-return-to (or return-to :hub))
              (reset! hub-screen-atom :session-list))

            state-map {:resources-atom resources-atom
                       :jobs-atom      jobs-atom
                       :sessions-atom  sessions-atom}]

        [:> Box {:class "flex flex-col bg-gray-1 px-10 pb-10 pt-10"
                 :style {:height "100vh" :box-sizing "border-box" :overflow "hidden"}}

         ;; ── Hub ──
         (when (= screen :hub)
           [hub/hub-view
            {:resources-atom       resources-atom
             :selected-ids-atom    selected-ids-atom
             :search-atom          search-atom
             :active-tab-atom      active-tab-atom
             :jobs-atom            jobs-atom
             :dismissed-job-ids-atom dismissed-job-ids
             :sessions-atom        sessions-atom
             :hovered-row-atom     hovered-row-atom
             :on-set-screen        set-screen!
             :on-open-bulk-admin   open-bulk-admin!
             :on-open-bulk-roles   open-bulk-roles!
             :on-open-bulk-import  #(set-screen! :bulk-import)}])

         ;; ── Bulk Admin ──
         (when (= screen :bulk-admin)
           [bulk-admin/bulk-admin-screen
            {:resources    @bulk-resources-atom
             :configs-atom bulk-configs-atom
             :initial-mode @bulk-admin-mode
             :on-cancel    #(set-screen! :hub)
             :on-apply     (fn [configs agent-id]
                             (let [job-id (data/start-job! state-map
                                                           {:type       :admin-setup
                                                            :targets    @bulk-resources-atom
                                                            :configs    configs
                                                            :agent-id   agent-id})]
                               (reset! viewing-job-id job-id)
                               (set-screen! :job-detail)))}])

         ;; ── Bulk Roles ──
         (when (= screen :bulk-roles)
           [bulk-roles/bulk-roles-screen
            {:resources      @bulk-resources-atom
             :initial-method @bulk-roles-method
             :on-cancel      #(set-screen! :hub)
             :on-apply       (fn [method roles-by-resource]
                               (let [job-id (data/start-job! state-map
                                                             {:type              :role-provision
                                                              :targets           @bulk-resources-atom
                                                              :roles-by-resource roles-by-resource})]
                                 (reset! viewing-job-id job-id)
                                 (set-screen! :job-detail)))}])

         ;; ── Bulk Import ──
         (when (= screen :bulk-import)
           [bulk-import/bulk-import-screen
            {:on-back    #(set-screen! :hub)
             :on-confirm (fn [new-resources]
                           (swap! resources-atom into new-resources))}])

         ;; ── Job Detail ──
         (when (= screen :job-detail)
           (let [job (some #(when (= (:id %) @viewing-job-id) %) @jobs-atom)]
             (when job
               [job-detail/job-detail-screen
                {:job                 job
                 :sessions            @sessions-atom
                 :on-back             #(set-screen! :hub)
                 :on-run-in-background #(set-screen! :hub)
                 :on-view-sessions    (fn [filter-opt]
                                        (navigate-sessions!
                                         {:job-id      (:id job)
                                          :resource-id (:resource-id filter-opt)
                                          :title       (if (:resource-name filter-opt)
                                                         (str "Sessions — " (:resource-name filter-opt))
                                                         (str "Sessions — " (:label job)))}
                                         :job-detail))}])))

         ;; ── Session List ──
         (when (= screen :session-list)
           (let [filt @session-filter-atom
                 filtered (filterv
                           (fn [s]
                             (and (or (nil? (:job-id filt))
                                      (= (:job-id filt) (:job-id s)))
                                  (or (nil? (:resource-id filt))
                                      (= (:resource-id filt) (:resource-id s)))))
                           @sessions-atom)]
             [session-list/session-list-screen
              {:sessions  filtered
               :title     (:title filt)
               :subtitle  (str (count filtered)
                               " session" (when (not= 1 (count filtered)) "s")
                               " · each triggered by a provisioning step")
               :on-back   #(set-screen! @session-return-to)}]))]))))
