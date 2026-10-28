(ns webapp.shared-ui.cmdk.events.command-palette
  (:require
   [clojure.string]
   [re-frame.core :as rf]
   [webapp.connections.views.hoop-cli-modal :as hoop-cli-modal]
   [webapp.shared-ui.cmdk.command-palette-constants :as constants]))

;; Event handlers for command palette

;; Debounce timer for search
(def search-debounce-timer (atom nil))

(defn- search-on? [db]
  (constants/search-enabled? (get-in db [:gateway->info :data :feature_flags])))

(rf/reg-event-fx
 :command-palette->open
 (fn [{:keys [db]} [_]]
   ;; The flags loaded with the app go stale when an admin toggles one.
   {:db (assoc-in db [:command-palette :open?] true)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri "/serverinfo"
                             :on-success #(rf/dispatch [:command-palette->set-feature-flags (:feature_flags %)])
                             :on-failure (fn [_])}]]]}))

(rf/reg-event-fx
 :command-palette->set-feature-flags
 (fn [{:keys [db]} [_ flags]]
   ;; Never creates :gateway->info :data: license gating reads its absence as "not loaded".
   (when (get-in db [:gateway->info :data])
     (cond-> {:db (assoc-in db [:gateway->info :data :feature_flags] flags)}
       (get-in db [:command-palette :open?])
       (assoc :fx [[:dispatch [:command-palette->search (get-in db [:command-palette :query])]]])))))

(rf/reg-event-fx
 :command-palette->close
 (fn [{:keys [db]} [_]]
   ;; Cancel pending search when closing
   (when @search-debounce-timer
     (js/clearTimeout @search-debounce-timer)
     (reset! search-debounce-timer nil))
   {:db (update-in  db [:command-palette]
                    merge {:open? false
                           :query ""
                           :current-page :main
                           :context {}
                           :search-results {:status :idle :data {}}})}))

(rf/reg-event-fx
 :command-palette->toggle
 (fn [{:keys [db]} [_]]
   (let [is-open? (get-in db [:command-palette :open?] false)]
     (if is-open?
       {:fx [[:dispatch [:command-palette->close]]]}
       {:fx [[:dispatch [:command-palette->open]]]}))))

(rf/reg-event-fx
 :command-palette->search
 (fn [{:keys [db]} [_ query]]
   (let [safe-query (or query "")
         trimmed-query (clojure.string/trim safe-query)
         current-page (get-in db [:command-palette :current-page] :main)
         ;; Search always enabled on main page
         search-enabled-pages #{:main}]
     ;; Cancel previous search timer
     (when @search-debounce-timer
       (js/clearTimeout @search-debounce-timer)
       (reset! search-debounce-timer nil))

     (cond
       ;; Other pages, just update query (no search)
       (not (contains? search-enabled-pages current-page))
       {:db (assoc-in db [:command-palette :query] safe-query)}

       ;; Query too short or search off, clear results
       (or (< (count trimmed-query) 2) (not (search-on? db)))
       {:db (-> db
                (assoc-in [:command-palette :query] safe-query)
                (assoc-in [:command-palette :search-results] {:status :idle :data {}}))}

       ;; Valid query, search with debounce
       :else
       (do
         ;; Set new debounce timer
         (reset! search-debounce-timer
                 (js/setTimeout
                  #(rf/dispatch [:command-palette->perform-search trimmed-query])
                  300)) ; 300ms debounce

         ;; Update query immediately but keep status as :searching for subtle feedback
         {:db (-> db
                  (assoc-in [:command-palette :query] safe-query)
                  (assoc-in [:command-palette :search-results :status] :searching))})))))

(rf/reg-event-fx
 :command-palette->perform-search
 (fn [{:keys [db]} [_ query]]
   ;; Debounced search execution; the flag may have turned off since it was scheduled
   (if (search-on? db)
     {:fx [[:dispatch
            [:fetch
             {:method "GET"
              :uri "/search"
              :query-params {:term query}
              :on-success #(rf/dispatch [:command-palette->search-success %])
              :on-failure #(rf/dispatch [:command-palette->search-failure %])}]]]}
     {:db (assoc-in db [:command-palette :search-results] {:status :idle :data {}})})))

(rf/reg-event-fx
 :command-palette->search-success
 (fn [{:keys [db]} [_ results]]
   {:db (assoc-in db [:command-palette :search-results]
                  (if (search-on? db)
                    {:status :ready
                     :data results}
                    {:status :idle :data {}}))}))

(rf/reg-event-fx
 :command-palette->search-failure
 (fn [{:keys [db]} [_ error]]
   (js/console.error "Command palette search error:" error)
   {:db (assoc-in db [:command-palette :search-results]
                  {:status :error
                   :data {}
                   :error error})}))

;; Navigate to specific pages
(rf/reg-event-fx
 :command-palette->navigate-to-page
 (fn [{:keys [db]} [_ page-type context]]
   (let [resource-name (:name context)
         ;; Clear old resource-roles data to prevent flashing
         db-with-cleared-roles (if (= page-type :resource-roles)
                                 (assoc-in db [:resources->resource-roles resource-name] {:loading true :data []})
                                 db)
         effects {:db (update-in db-with-cleared-roles [:command-palette] merge {:current-page page-type
                                                                                 :context context
                                                                                 :query ""
                                                                                 :search-results {:status :idle :data {}}})}]
     ;; If navigating to resource-roles, fetch roles with 500ms timeout to avoid flashing
     (if (= page-type :resource-roles)
       (assoc effects :fx [[:dispatch-later {:ms 500
                                             :dispatch [:resources->get-resource-roles
                                                        resource-name
                                                        {:force-refresh? true}]}]])
       effects))))

;; Go back to main page
(rf/reg-event-fx
 :command-palette->back
 (fn [{:keys [db]} [_]]
   {:db (update-in db [:command-palette]
                   merge {:current-page :main
                          :context {}
                          :query ""
                          :search-results {:status :idle :data {}}})}))

;; Execute action based on type
(rf/reg-event-fx
 :command-palette->execute-action
 (fn [_ [_ action]]
   (case (:action action)
     :navigate
     {:fx [[:dispatch [:command-palette->close]]
           [:dispatch [:navigate (:route action)]]]}

    :web-terminal
    {:fx [[:dispatch [:command-palette->close]]
          [:dispatch [:navigate :editor-plugin {:role (:connection-name action)}]]]}

     :local-terminal
     ;; Same logic as connection-list: open hoop-cli modal
     {:fx [[:dispatch [:command-palette->close]]
           [:dispatch-later {:ms 100
                             :dispatch [:modal->open {:content [hoop-cli-modal/main (:connection-name action)]
                                                      :maxWidth "1100px"
                                                      :class "overflow-hidden"}]}]]}

     :test-connection
     {:fx [[:dispatch [:command-palette->close]]
           [:dispatch [:connections->test-connection (:connection-name action)]]]}


     :open-native-client
     {:fx [[:dispatch [:command-palette->close]]
           [:dispatch-later {:ms 100
                             :dispatch [:native-client-access->start-flow (:connection-name action)]}]]}

     :configure
     ;; Same logic as connection-list: get plugins + navigate
     {:fx [[:dispatch [:command-palette->close]]
           [:dispatch [:plugins->get-my-plugins]]
           [:dispatch [:navigate :edit-connection {} :connection-name (:connection-name action)]]]}

     :external
     {:fx [[:dispatch [:command-palette->close]]]}

     ;; Default
     {:fx [[:dispatch [:command-palette->close]]]})))
