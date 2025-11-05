(ns webapp.connections.views.setup.events.effects
  (:require
   [clojure.string :as str]
   [re-frame.core :as rf]
   [webapp.connections.views.setup.events.process-form :as process-form]
   [webapp.connections.views.setup.tags-utils :as tags-utils]))

(rf/reg-event-db
 :connection-setup/initialize-state
 (fn [db [_ initial-data]]
   (println "initial-data" initial-data)
   (if initial-data
     (assoc db :connection-setup (assoc initial-data
                                        :ssh-auth-method (get initial-data :ssh-auth-method "password")
                                        :command-args (get initial-data :command-args [{"value" "bash" "label" "bash"}])
                                        :command "bash"))
     (assoc db :connection-setup {:ssh-auth-method "password"
                                  :command-args [{"value" "bash" "label" "bash"}]
                                  :command "bash"}))))

(defn filter-valid-tags
  [tags]
  (filterv (fn [{:keys [key value]}]
             (and key
                  (not (str/blank? (if (string? key) key (str key))))
                  value
                  (not (str/blank? (if (string? value) value (str value))))))
           tags))

(rf/reg-event-fx
 :connection-setup/select-type
 (fn [{:keys [db]} [_ connection-type]]
   {:db (assoc-in db [:connection-setup :type] connection-type)
    :fx [[:dispatch [:connection-setup/next-step :credentials]]]}))

(rf/reg-event-fx
 :connection-setup/initialize-from-catalog
 (fn [{:keys [db]} [_ {:keys [type subtype app-type command]}]]
   {:db (update db :connection-setup merge {:type type
                                            :subtype subtype
                                            :app-type app-type
                                            :metadata-command-args command
                                            :current-step :credentials
                                            :from-catalog? true})
    :fx [[:dispatch [:connection-setup/select-connection type subtype]]]}))

(rf/reg-event-fx
 :connection-tags/fetch
 (fn [{:keys [db]} _]
   {:fx [[:dispatch [:fetch {:method "GET"
                             :uri "/connection-tags"
                             :on-success (fn [tags]
                                           (rf/dispatch [:connection-tags/set tags]))}]]]
    :db (assoc-in db [:connection-tags :loading?] true)}))

(rf/reg-event-fx
 :connection-setup/set-current-key
 (fn [{:keys [db]} [_ current-key]]
   (let [full-key (when current-key (.-value current-key))
         label (when full-key
                 (tags-utils/extract-label full-key))
         tags-data (get-in db [:connection-tags :data])
         available-values (when (and full-key tags-data)
                            (tags-utils/get-values-for-key tags-data full-key))]
     {:db (-> db
              (assoc-in [:connection-setup :tags :current-key] current-key)
              (assoc-in [:connection-setup :tags :current-full-key] full-key)
              (assoc-in [:connection-setup :tags :current-label] label)
              (assoc-in [:connection-setup :tags :available-values] (or available-values []))
              (assoc-in [:connection-setup :tags :current-value] nil))})))

(rf/reg-event-fx
 :connection-setup/update-tag-key
 (fn [{:keys [db]} [_ index selected-option]]
   (let [full-key (when selected-option (.-value selected-option))
         label (when full-key
                 (tags-utils/extract-label full-key))
         tags-data (get-in db [:connection-tags :data])
         available-values (when (and full-key
                                     (not (str/blank? full-key))
                                     tags-data)
                            (tags-utils/get-values-for-key tags-data full-key))]
     {:db (-> db
              (assoc-in [:connection-setup :tags :data index :key] full-key)
              (assoc-in [:connection-setup :tags :data index :label] label)
              (assoc-in [:connection-setup :tags :data index :value] nil)
              (assoc-in [:connection-setup :tags :available-values-for-index index] (or available-values [])))})))

(rf/reg-event-fx
 :connection-setup/submit
 (fn [{:keys [db]} _]
   (let [current-env-key (get-in db [:connection-setup :credentials :current-key])
         current-env-value (get-in db [:connection-setup :credentials :current-value])
         current-file-name (get-in db [:connection-setup :credentials :current-file-name])
         current-file-content (get-in db [:connection-setup :credentials :current-file-content])

         current-tag-key (get-in db [:connection-setup :tags :current-key])
         current-tag-value (get-in db [:connection-setup :tags :current-value])

         db-with-current-creds (cond-> db
                                 (and (not (empty? current-env-key))
                                      (not (empty? current-env-value)))
                                 (update-in [:connection-setup :credentials :environment-variables]
                                            #(conj (or % []) {:key current-env-key :value current-env-value}))

                                 (and (not (empty? current-file-name))
                                      (not (empty? current-file-content)))
                                 (update-in [:connection-setup :credentials :configuration-files]
                                            #(conj (or % []) {:key current-file-name :value current-file-content})))

         db-with-current-tag (cond-> db-with-current-creds
                               (and current-tag-key (.-value current-tag-key))
                               (update-in [:connection-setup :tags :data]
                                          #(conj (or % [])
                                                 {:key (.-value current-tag-key)
                                                  :value (if current-tag-value
                                                           (.-value current-tag-value)
                                                           "")})))

         existing-tags (get-in db-with-current-tag [:connection-setup :tags :data] [])
         valid-tags (filter-valid-tags existing-tags)

         db-with-processed-tags (assoc-in db-with-current-tag [:connection-setup :tags :data] valid-tags)

         payload (process-form/process-payload db-with-processed-tags)]

     {:fx [[:dispatch [:connections->create-connection payload]]
           [:dispatch-later {:ms 500
                             :dispatch [:connection-setup/initialize-state nil]}]]})))
