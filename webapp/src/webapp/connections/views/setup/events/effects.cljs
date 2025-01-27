(ns webapp.connections.views.setup.events.effects
  (:require [re-frame.core :as rf]
            [webapp.connections.helpers :as helpers]
            [webapp.connections.views.setup.events.initial-state :as initial-state]))

;; Initialize app state
(rf/reg-event-fx
 :connection-setup/initialize
 (fn [{:keys [db]} _]
   {:db (assoc db :connection-setup initial-state/initial-state)}))

;; Main effects that change multiple parts of state or interact with external systems
(rf/reg-event-fx
 :connection-setup/select-type
 (fn [{:keys [db]} [_ connection-type]]
   {:db (assoc-in db [:connection-setup :type] connection-type)
    :fx [[:dispatch [:connection-setup/update-step :credentials]]]}))

(rf/reg-event-fx
 :connection-setup/submit
 (fn [{:keys [db]} _]
   (let [connection-type (get-in db [:connection-setup :database-type])
         connection-name (get-in db [:connection-setup :name])
         tags (get-in db [:connection-setup :tags])
         config (get-in db [:connection-setup :config])
         env-vars (get-in db [:connection-setup :credentials :environment-variables] [])
         config-files (get-in db [:connection-setup :credentials :configuration-files] [])
         command (get-in db [:connection-setup :credentials :command])
         review-groups (get-in config [:review-groups])
         data-masking-types (get-in config [:data-masking-types])

         secret (clj->js
                 (merge
                  (helpers/config->json env-vars "envvar:")
                  (when (seq config-files)
                    (helpers/config->json config-files "filesystem:"))))

         payload {:type "database"
                  :subtype connection-type
                  :name connection-name
                  :tags (when (seq tags)
                          (mapv #(get % "value") tags))
                  :secret secret
                  :command (if "database"
                             []
                             (when command
                               (or (re-seq #"'.*?'|\".*?\"|\S+|\t" command) [])))
                  :access_schema (if (:database-schema config) "enabled" "disabled")
                  :access_mode_runbooks (if (get-in config [:access-modes :runbooks]) "enabled" "disabled")
                  :access_mode_exec (if (get-in config [:access-modes :web]) "enabled" "disabled")
                  :access_mode_connect (if (get-in config [:access-modes :native]) "enabled" "disabled")
                  :redact_enabled (:data-masking config false)
                  :redact_types (when (seq data-masking-types)
                                  (mapv #(get % "value") data-masking-types))
                  :reviewers (when (seq review-groups)
                               (mapv #(get % "value") review-groups))}]

     {:fx [[:dispatch [:show-snackbar {:level :success
                                       :text "Connection created successfully!"}]]]})))
