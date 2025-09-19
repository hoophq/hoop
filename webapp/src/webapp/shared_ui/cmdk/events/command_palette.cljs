(ns webapp.shared-ui.cmdk.events.command-palette
  (:require
   [clojure.string]
   [re-frame.core :as rf]
   [webapp.connections.views.hoop-cli-modal :as hoop-cli-modal]))

;; Event handlers for command palette

;; Debounce timer for search
(def search-debounce-timer (atom nil))

(rf/reg-event-fx
 :command-palette->open
 (fn [{:keys [db]} [_]]
   {:db (assoc-in db [:command-palette :open?] true)}))

(rf/reg-event-fx
 :command-palette->close
 (fn [{:keys [db]} [_]]
   ;; Cancel pending search when closing
   (when @search-debounce-timer
     (js/clearTimeout @search-debounce-timer)
     (reset! search-debounce-timer nil))
   {:db (-> db
            (assoc-in [:command-palette :open?] false)
            (assoc-in [:command-palette :query] "")
            (assoc-in [:command-palette :current-page] :main)
            (assoc-in [:command-palette :context] {})
            (assoc-in [:command-palette :search-results] {:status :idle :data {}}))}))

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

     (if (contains? search-enabled-pages current-page)
       ;; Main page - search enabled
       (if (< (count trimmed-query) 2)
         ;; Query too short, clear results
         {:db (-> db
                  (assoc-in [:command-palette :query] safe-query)
                  (assoc-in [:command-palette :search-results] {:status :idle :data {}}))}
         ;; Valid query, search with debounce
         (do
           ;; Set new debounce timer
           (reset! search-debounce-timer
                   (js/setTimeout
                    #(rf/dispatch [:command-palette->perform-search trimmed-query])
                    300)) ; 300ms debounce

           ;; Update query immediately but keep status as :searching for subtle feedback
           {:db (-> db
                    (assoc-in [:command-palette :query] safe-query)
                    (assoc-in [:command-palette :search-results :status] :searching))}))
       ;; Other pages, just update query (no search)
       {:db (assoc-in db [:command-palette :query] safe-query)}))))

(rf/reg-event-fx
 :command-palette->perform-search
 (fn [_ [_ query]]
   ;; Debounced search execution
   {:fx [[:dispatch
          [:fetch
           {:method "GET"
            :uri "/search"
            :query-params {:term query}
            :on-success #(rf/dispatch [:command-palette->search-success %])
            :on-failure #(rf/dispatch [:command-palette->search-failure %])}]]]}))

(rf/reg-event-fx
 :command-palette->search-success
 (fn [{:keys [db]} [_ results]]
   {:db (assoc-in db [:command-palette :search-results]
                  {:status :ready
                   :data results})}))

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
   {:db (-> db
            (assoc-in [:command-palette :current-page] page-type)
            (assoc-in [:command-palette :context] context)
            (assoc-in [:command-palette :query] "")
            (assoc-in [:command-palette :search-results] {:status :idle :data {}}))}))

;; Go back to main page
(rf/reg-event-fx
 :command-palette->back
 (fn [{:keys [db]} [_]]
   {:db (-> db
            (assoc-in [:command-palette :current-page] :main)
            (assoc-in [:command-palette :context] {})
            (assoc-in [:command-palette :query] "")
            (assoc-in [:command-palette :search-results] {:status :idle :data {}}))}))

;; Execute action based on type
(rf/reg-event-fx
 :command-palette->execute-action
 (fn [_ [_ action]]
   (case (:action action)
     :navigate
     {:fx [[:dispatch [:command-palette->close]]
           [:dispatch [:navigate (:route action)]]]}

     :web-terminal
     ;; Same logic as connection-list: localStorage + navigate
     (do
       (js/localStorage.setItem "selected-connection"
                                (str {:name (:connection-name action)
                                      :id (:connection-id action)}))
       {:fx [[:dispatch [:command-palette->close]]
             [:dispatch [:navigate :editor-plugin-panel]]]})

     :local-terminal
     ;; Same logic as connection-list: open hoop-cli modal
     {:fx [[:dispatch [:command-palette->close]]
           [:dispatch [:modal->open {:content [hoop-cli-modal/main (:connection-name action)]
                                     :maxWidth "1100px"
                                     :class "overflow-hidden"}]]]}

     :configure
     ;; Same logic as connection-list: get plugins + navigate
     {:fx [[:dispatch [:command-palette->close]]
           [:dispatch [:plugins->get-my-plugins]]
           [:dispatch [:navigate :edit-connection {} :connection-name (:connection-name action)]]]}

     :external
     {:fx [[:dispatch [:command-palette->close]]]}

     ;; Default
     {:fx [[:dispatch [:command-palette->close]]]})))
