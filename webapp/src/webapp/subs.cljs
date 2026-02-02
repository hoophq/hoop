(ns webapp.subs
  (:require
   [clojure.string :as cs]
   [re-frame.core :as re-frame]))

(re-frame/reg-sub
 :user
 (fn [db]
   (:user db)))

(re-frame/reg-sub
 ::active-panel
 (fn [db _]
   (:active-panel db)))

(re-frame/reg-sub
 :users
 (fn [db _]
   (:users db)))

(re-frame/reg-sub
 :user-groups
 (fn [db _]
   (:user-groups db)))

(re-frame/reg-sub
 :connections
 (fn [db _]
   (:connections db)))

(re-frame/reg-sub
 :connections->pagination
 (fn [db _]
   (:connections->pagination db)))

(re-frame/reg-sub
 :connections->connection-details
 (fn [db _]
   (:connections->connection-details db)))

(re-frame/reg-sub
 ::database-schema
 (fn [db _]
   (:database-schema db)))

(re-frame/reg-sub
 :agents
 (fn [db _]
   (:agents db)))

(re-frame/reg-sub
 :reviews
 (fn [db _]
   (:reviews db)))

(re-frame/reg-sub
 ::page-loader
 (fn [db _]
   {:status (:page-loader-status db)}))

(re-frame/reg-sub
 :snackbar
 (fn [db _]
   {:status (:snackbar-status db)
    :level (:snackbar-level db)
    :text (:snackbar-text db)}))

(re-frame/reg-sub
 :dialog
 (fn [db _]
   {:status (:dialog-status db)
    :on-success (:dialog-on-success db)
    :text (:dialog-text db)
    :title (:dialog-title db)}))

(re-frame/reg-sub
 :modal
 (fn [db _]
   (let [modal (:modal db)]
     {:status (:status modal)
      :component (:component modal)
      :size (:size modal)
      :on-click-out (:on-click-out modal)})))


(re-frame/reg-sub
 :draggable-card->modal
 (fn [db _]
   (let [modal (:draggable-card->modal db)]
     {:status (:status modal)
      :component (:component modal)
      :size (:size modal)
      :on-click-out (:on-click-out modal)})))

(re-frame/reg-sub
 :plugins->my-plugins
 (fn [db _]
   (:plugins->my-plugins db)))

(re-frame/reg-sub
 :plugins->plugin-details
 (fn [db _]
   (:plugins->plugin-details db)))

(re-frame/reg-sub
 :audit
 (fn [db _]
   (:audit db)))

(re-frame/reg-sub
 :audit->session-details
 (fn [db _]
   (:audit->session-details db)))

(re-frame/reg-sub
 :audit->session-logs
 (fn [db _]
   (:audit->session-logs db)))

(re-frame/reg-sub
 :connections->test-connection
 (fn [db _]
   (:connections->test-connection db)))

(re-frame/reg-sub
 :connections->metadata
 (fn [db _]
   (get-in db [:connections :metadata :data])))

(re-frame/reg-sub
 :connections->metadata-loading?
 (fn [db _]
   (get-in db [:connections :metadata :loading] false)))

(re-frame/reg-sub
 :connections->metadata-error
 (fn [db _]
   (get-in db [:connections :metadata :error])))

(re-frame/reg-sub
 :users->current-user
 (fn [db _]
   (:users->current-user db)))

(re-frame/reg-sub
 :reviews-plugin->reviews
 (fn [db _]
   (:reviews-plugin->reviews db)))

(re-frame/reg-sub
 :reviews-plugin->review-details
 (fn [db _]
   (:reviews-plugin->review-details db)))

(re-frame/reg-sub
 :editor-plugin->script
 (fn [db _]
   (:editor-plugin->script db)))


(re-frame/reg-sub
 :indexer-plugin->search
 (fn [db _]
   (:indexer-plugin->search db)))


(re-frame/reg-sub
 :sidebar-desktop
 (fn [db _]
   (:sidebar-desktop db)))

(re-frame/reg-sub
 :sidebar-mobile
 (fn [db _]
   (:sidebar-mobile db)))

(re-frame/reg-sub
 :segment->analytics
 (fn [db _]
   (:segment->analytics db)))

(re-frame/reg-sub
 :organization->api-key
 (fn [db _]
   (:organization->api-key db)))

(re-frame/reg-sub
 :routes->route
 (fn [db _]
   (:routes->route db)))

(re-frame/reg-sub
 :gateway->info
 (fn [db _]
   (:gateway->info db)))

(re-frame/reg-sub
 :gateway->public-info
 (fn [db _]
   (:gateway->public-info db)))

;; Active panel state
(re-frame/reg-sub
 :webclient->active-panel
 (fn [db _]
   (get db :webclient->active-panel nil)))

(re-frame/reg-sub
 :audit->filtered-session-by-id
 (fn [db _]
   (:audit->filtered-session-by-id db)))

(re-frame/reg-sub
 :audit->filtered-session-search-term
 (fn [db _]
   (get-in db [:audit->filtered-session-by-id :search-term] "")))

(re-frame/reg-sub
 :audit->filtered-sessions-by-id-filtered
 :<- [:audit->filtered-session-by-id]
 :<- [:audit->filtered-session-search-term]
 (fn [[session-state search-term] _]
   (let [sessions (:data session-state)
         search-term-lower (cs/lower-case search-term)]
     (if (cs/blank? search-term)
       sessions
       (filter (fn [session]
                 (or (cs/includes? (cs/lower-case (or (:connection session) "")) search-term-lower)
                     (cs/includes? (cs/lower-case (or (:type session) "")) search-term-lower)
                     (cs/includes? (cs/lower-case (or (:id session) "")) search-term-lower)
                     (cs/includes? (cs/lower-case (or (:user_name session) "")) search-term-lower)))
               sessions)))))

(re-frame/reg-sub
 :new-dialog
 (fn [db _]
   (:dialog db)))

(re-frame/reg-sub
 :reports->session
 (fn [db _]
   (:reports->session db)))

(re-frame/reg-sub
 :reports->redacted-data-by-date
 (fn [db _]
   (:reports->redacted-data-by-date db)))

(re-frame/reg-sub
 :reports->review-data-by-date
 (fn [db _]
   (:reports->review-data-by-date db)))

(re-frame/reg-sub
 :reports->today-redacted-data
 (fn [db _]
   (:reports->today-redacted-data db)))

(re-frame/reg-sub
 :reports->today-review-data
 (fn [db _]
   (:reports->today-review-data db)))

(re-frame/reg-sub
 :reports->today-session-data
 (fn [db _]
   (:reports->today-session-data db)))

(re-frame/reg-sub
 :modal-radix
 (fn [db _]
   (:modal-radix db)))

(re-frame/reg-sub
 :jira-integration->details
 (fn [db _]
   (:jira-integration->details db)))

;; Command Palette subscriptions
(re-frame/reg-sub
 :command-palette
 (fn [db _]
   (:command-palette db)))

(re-frame/reg-sub
 :command-palette->search-results
 (fn [db _]
   (get-in db [:command-palette :search-results])))
