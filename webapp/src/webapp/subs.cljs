(ns webapp.subs
  (:require
   [re-frame.core :as re-frame]
   [webapp.formatters :as f]))

(re-frame/reg-sub
 :user
 (fn [db]
   (:user db)))

(re-frame/reg-sub
 ::active-panel
 (fn [db _]
   (:active-panel db)))

(re-frame/reg-sub
 :feature-flags
 (fn [db _]
   (:feature-flags db)))

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
 :all-connections
 (fn [db _]
   (:all-connections db)))

(re-frame/reg-sub
 :connections->connection-details
 (fn [db _]
   (:connections->connection-details db)))

(re-frame/reg-sub
 :runbooks-plugin->runbooks-by-connection
 (fn [db _]
   (:runbooks-plugin->runbooks-by-connection db)))

(re-frame/reg-sub
 :runbooks->settings
 (fn [db _]
   (:runbooks->settings db)))

(re-frame/reg-sub
 :runbooks->github-connection-info
 (fn [db _]
   (:runbooks->github-connection-info db)))

(re-frame/reg-sub
 :new-task
 (fn [db _]
   (:new-task db)))

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
 :templates
 (fn [db _]
   (:templates db)))

(re-frame/reg-sub
 :templates->filtered-templates
 (fn [db _]
   (:templates->filtered-templates db)))

(re-frame/reg-sub
 :templates->selected-template
 (fn [db _]
   (:templates->selected-template db)))

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
 :draggable-card
 (fn [db _]
   (let [draggable-card (:draggable-card db)]
     {:status (:status draggable-card)
      :component (:component draggable-card)
      :on-click-close (:on-click-close draggable-card)
      :on-click-expand (:on-click-expand draggable-card)})))

(re-frame/reg-sub
 :draggable-card->modal
 (fn [db _]
   (let [modal (:draggable-card->modal db)]
     {:status (:status modal)
      :component (:component modal)
      :size (:size modal)
      :on-click-out (:on-click-out modal)})))

(re-frame/reg-sub
 :new-task->selected-connection
 (fn [db _]
   (:new-task->selected-connection db)))

(re-frame/reg-sub
 :new-task->editor-language
 (fn [db _]
   (:new-task->editor-language db)))

(re-frame/reg-sub
 :dashboard->running-queries
 (fn [db _]
   (:dashboard->running-queries db)))

(re-frame/reg-sub
 :dashboard->waiting-review-queries
 (fn [db _]
   (:dashboard->waiting-review-queries db)))

(re-frame/reg-sub
 :dashboard->status-total-by-date
 (fn [db _]
   (:dashboard->status-total-by-date db)))

(re-frame/reg-sub
 :dashboard->slower-queries
 (fn [db _]
   (:dashboard->slower-queries db)))

(re-frame/reg-sub
 :dashboard->filters
 (fn [db _]
   (:dashboard->filters db)))

(re-frame/reg-sub
 :plugins->active-tab
 (fn [db _]
   (:plugins->active-tab db)))

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
 ::connections->updating-connection
 (fn [db _]
   (:connections->updating-connection db)))

(re-frame/reg-sub
 :connections->connection-connected
 (fn [db _]
   (:connections->connection-connected db)))

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
 :editor-plugin->current-connection
 (fn [db _]
   (:editor-plugin->current-connection db)))

(re-frame/reg-sub
 :editor-plugin->select-language
 (fn [db _]
   (:editor-plugin->select-language db)))

(re-frame/reg-sub
 :ask-ai->question-responses
 (fn [db _]
   (:ask-ai->question-responses db)))

(re-frame/reg-sub
 :runbooks-plugin->runbooks
 (fn [db _]
   (:runbooks-plugin->runbooks db)))

(re-frame/reg-sub
 :runbooks-plugin->selected-runbooks
 (fn [db _]
   (:runbooks-plugin->selected-runbooks db)))

(re-frame/reg-sub
 :runbooks-plugin->filtered-runbooks
 (fn [db _]
   (:runbooks-plugin->filtered-runbooks db)))

(re-frame/reg-sub
 :use-cases->list
 (fn [db _]
   (:use-cases->list db)))

(re-frame/reg-sub
 :indexer-plugin->search
 (fn [db _]
   (:indexer-plugin->search db)))

(re-frame/reg-sub
 :hoop-app->my-configs
 (fn [db _]
   (:hoop-app->my-configs db)))

(re-frame/reg-sub
 :hoop-app->running?
 (fn [db _]
   (:hoop-app->running? db)))

(re-frame/reg-sub
 :policies->list
 (fn [db _]
   (:policies->list db)))

(re-frame/reg-sub
 :agents-embedded
 (fn [db _]
   (:agents-embedded db)))

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
 :editor-plugin->run-connection-list
 (fn [db _]
   (:editor-plugin->run-connection-list db)))

(re-frame/reg-sub
 :editor-plugin->run-connection-list-selected
 (fn [db _]
   (:editor-plugin->run-connection-list-selected db)))

(re-frame/reg-sub
 :editor-plugin->filtered-run-connection-list
 (fn [db _]
   (:editor-plugin->filtered-run-connection-list db)))

(re-frame/reg-sub
 :editor-plugin->connections-exec-list
 (fn [db _]
   (:editor-plugin->connections-exec-list db)))

(re-frame/reg-sub
 :editor-plugin->connections-runbook-list
 (fn [db _]
   (:editor-plugin->connections-runbook-list db)))

(re-frame/reg-sub
 :audit->filtered-session-by-id
 (fn [db _]
   (:audit->filtered-session-by-id db)))

(re-frame/reg-sub
 :new-dialog
 (fn [db _]
   (:dialog db)))

(re-frame/reg-sub
 :reports->session
 (fn [db _]
   (:reports->session db)))

(re-frame/reg-sub
 :reports->redact-data-by-date
 (fn [db _]
   (:reports->redact-data-by-date db)))

(re-frame/reg-sub
 :reports->review-data-by-date
 (fn [db _]
   (:reports->review-data-by-date db)))

(re-frame/reg-sub
 :reports->today-redact-data
 (fn [db _]
   (:reports->today-redact-data db)))

(re-frame/reg-sub
 :reports->today-review-data
 (fn [db _]
   (:reports->today-review-data db)))

(re-frame/reg-sub
 :reports->today-session-data
 (fn [db _]
   (:reports->today-session-data db)))
