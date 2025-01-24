(ns webapp.connections.views.setup.events
  (:require
   [re-frame.core :as rf]
   [webapp.connections.helpers :as helpers]))

;; Events
(rf/reg-event-fx
 :connection-setup/select-type
 (fn [{:keys [db]} [_ connection-type]]
   {:db (assoc-in db [:connection-setup :type] connection-type)
    :fx [[:dispatch [:connection-setup/update-step :credentials]]]}))

(rf/reg-event-db
 :connection-setup/select-subtype
 (fn [db [_ subtype]]
   (assoc-in db [:connection-setup :subtype] subtype)))

(rf/reg-event-db
 :connection-setup/update-step
 (fn [db [_ step]]
   (assoc-in db [:connection-setup :current-step] step)))

(rf/reg-event-db
 :connection-setup/update-credentials
 (fn [db [_ field value]]
   (assoc-in db [:connection-setup :credentials field] value)))

;; Server-specific events
(rf/reg-event-db
 :connection-setup/select-app-type
 (fn [db [_ app-type]]
   (-> db
       (assoc-in [:connection-setup :app-type] app-type)
       (assoc-in [:connection-setup :current-step] :os-type))))

(rf/reg-event-db
 :connection-setup/select-os-type
 (fn [db [_ os-type]]
   (-> db
       (assoc-in [:connection-setup :os-type] os-type)
       (assoc-in [:connection-setup :current-step] :credentials))))

(rf/reg-event-db
 :connection-setup/add-environment-variable
 (fn [db [_]]
   (let [current-key (get-in db [:connection-setup :credentials :current-key])
         current-value (get-in db [:connection-setup :credentials :current-value])
         current-vars (get-in db [:connection-setup :credentials :environment-variables] [])]
     (-> db
         (update-in [:connection-setup :credentials :environment-variables]
                    #(conj (or % []) {:key current-key :value current-value}))
         (assoc-in [:connection-setup :credentials :current-key] "")
         (assoc-in [:connection-setup :credentials :current-value] "")))))

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

     (js/console.log "Payload:" (clj->js payload))

     {:fx [[:dispatch [:show-snackbar {:level :success
                                       :text "Connection created successfully!"}]]]})))

(rf/reg-event-db
 :connection-setup/next-step
 (fn [db [_]]
   (let [current-step (get-in db [:connection-setup :current-step])]
     (assoc-in db [:connection-setup :current-step]
               (case current-step
                 :resource :additional-config
                 :additional-config :resource  ;; fallback
                 :resource)))))

(rf/reg-event-db
 :connection-setup/go-back
 (fn [db [_]]
   (let [current-subtype (get-in db [:connection-setup :subtype])
         app-type (get-in db [:connection-setup :app-type])
         os-type (get-in db [:connection-setup :os-type])]
     (cond
       (and (= current-subtype "console") app-type os-type)
       (-> db
           (assoc-in [:connection-setup :os-type] nil)
           (assoc-in [:connection-setup :current-step] :os-type))

       os-type
       (-> db
           (assoc-in [:connection-setup :os-type] nil)
           (assoc-in [:connection-setup :current-step] :os-type))

       app-type
       (-> db
           (assoc-in [:connection-setup :app-type] nil)
           (assoc-in [:connection-setup :current-step] :app-type))

       current-subtype
       (-> db
           (assoc-in [:connection-setup :subtype] nil)
           (assoc-in [:connection-setup :current-step] :type))

       :else
       (assoc-in db [:connection-setup :type] nil)))))

(rf/reg-event-db
 :connection-setup/select-database-type
 (fn [db [_ db-type]]
   (-> db
       (assoc-in [:connection-setup :database-type] db-type)
       (assoc-in [:connection-setup :current-step] :database-credentials))))

(rf/reg-event-db
 :connection-setup/update-database-credentials
 (fn [db [_ field value]]
   (assoc-in db [:connection-setup :database-credentials field] value)))

(rf/reg-event-db
 :connection-setup/toggle-review
 (fn [db [_]]
   (update-in db [:connection-setup :config :review] not)))

(rf/reg-event-db
 :connection-setup/toggle-data-masking
 (fn [db [_]]
   (update-in db [:connection-setup :config :data-masking] not)))

(rf/reg-event-db
 :connection-setup/toggle-database-schema
 (fn [db [_]]
   (update-in db [:connection-setup :config :database-schema] not)))

(rf/reg-event-db
 :connection-setup/toggle-access-mode
 (fn [db [_ mode]]
   (update-in db [:connection-setup :config :access-modes mode] not)))

(rf/reg-event-db
 :connection-setup/set-name
 (fn [db [_ value]]
   (assoc-in db [:connection-setup :name] value)))

(rf/reg-event-db
 :connection-setup/set-tags
 (fn [db [_ value]]
   (assoc-in db [:connection-setup :tags] value)))

(rf/reg-event-db
 :connection-setup/set-tags-input
 (fn [db [_ value]]
   (assoc-in db [:connection-setup :tags-input] value)))

(rf/reg-event-db
 :connection-setup/set-review-groups
 (fn [db [_ groups]]
   (assoc-in db [:connection-setup :config :review-groups] groups)))

(rf/reg-event-db
 :connection-setup/set-data-masking-types
 (fn [db [_ types]]
   (assoc-in db [:connection-setup :config :data-masking-types] types)))

(rf/reg-event-db
 :connection-setup/add-configuration-file
 (fn [db [_ file]]
   (update-in db [:connection-setup :configuration-files]
              conj file)))

(rf/reg-event-db
 :connection-setup/select-network-type
 (fn [db [_ network-type]]
   (assoc-in db [:connection-setup :network-type] network-type)))

(rf/reg-event-db
 :connection-setup/update-network-credentials
 (fn [db [_ field value]]
   (assoc-in db [:connection-setup :network-credentials field] value)))

(rf/reg-event-db
 :connection-setup/set-agent-id
 (fn [db [_ agent-id]]
   (assoc-in db [:connection-setup :agent-id] agent-id)))

;; Subscriptions
(rf/reg-sub
 :connection-setup/current-step
 (fn [db _]
   (get-in db [:connection-setup :current-step])))

(rf/reg-sub
 :connection-setup/connection-type
 (fn [db _]
   (get-in db [:connection-setup :type])))

(rf/reg-sub
 :connection-setup/connection-subtype
 (fn [db _]
   (get-in db [:connection-setup :subtype])))

(rf/reg-sub
 :connection-setup/credentials
 (fn [db _]
   (get-in db [:connection-setup :credentials])))

(rf/reg-sub
 :connection-setup/app-type
 (fn [db _]
   (get-in db [:connection-setup :app-type])))

(rf/reg-sub
 :connection-setup/os-type
 (fn [db _]
   (get-in db [:connection-setup :os-type])))

(rf/reg-sub
 :connection-setup/config
 (fn [db]
   (get-in db [:connection-setup :config])))

(rf/reg-sub
 :connection-setup/database-type
 (fn [db _]
   (get-in db [:connection-setup :database-type])))

(rf/reg-sub
 :connection-setup/database-credentials
 (fn [db _]
   (get-in db [:connection-setup :database-credentials])))

(rf/reg-sub
 :connection-setup/review
 :<- [:connection-setup/config]
 (fn [config]
   (:review config false)))

(rf/reg-sub
 :connection-setup/data-masking
 :<- [:connection-setup/config]
 (fn [config]
   (:data-masking config false)))

(rf/reg-sub
 :connection-setup/database-schema
 :<- [:connection-setup/config]
 (fn [config]
   (:database-schema config false)))

(rf/reg-sub
 :connection-setup/access-modes
 :<- [:connection-setup/config]
 (fn [config]
   (get-in config [:access-modes] {:runbooks true, :native true, :web true})))

(rf/reg-sub
 :connection-setup/name
 (fn [db]
   (get-in db [:connection-setup :name])))

(rf/reg-sub
 :connection-setup/tags
 (fn [db]
   (get-in db [:connection-setup :tags])))

(rf/reg-sub
 :connection-setup/tags-input
 (fn [db]
   (get-in db [:connection-setup :tags-input])))

(rf/reg-sub
 :connection-setup/review-groups
 :<- [:connection-setup/config]
 (fn [config]
   (get-in config [:review-groups] [])))

(rf/reg-sub
 :connection-setup/data-masking-types
 :<- [:connection-setup/config]
 (fn [config]
   (get-in config [:data-masking-types] [])))

(rf/reg-sub
 :connection-setup/environment-variables
 (fn [db]
   (get-in db [:connection-setup :environment-variables] [])))

(rf/reg-sub
 :connection-setup/configuration-files
 (fn [db]
   (get-in db [:connection-setup :configuration-files] [])))

(rf/reg-sub
 :connection-setup/network-type
 (fn [db]
   (get-in db [:connection-setup :network-type])))

(rf/reg-sub
 :connection-setup/network-credentials
 (fn [db]
   (get-in db [:connection-setup :network-credentials] {})))

(rf/reg-sub
 :connection-setup/agent-id
 (fn [db]
   (get-in db [:connection-setup :agent-id])))
