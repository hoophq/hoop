(ns webapp.connections.views.setup.events.effects
  (:require
   [clojure.string :as str]
   [re-frame.core :as rf]
   [webapp.connections.views.setup.tags-utils :as tags-utils]))

(rf/reg-event-db
 :connection-setup/initialize-state
 (fn [db [_ initial-data]]
   (if initial-data
     (let [connection-method (get initial-data :connection-method "manual-input")
           secrets-manager-provider (get initial-data :secrets-manager-provider)]
       (assoc db :connection-setup (assoc initial-data
                                          :ssh-auth-method (get initial-data :ssh-auth-method "password")
                                          :command-args (get initial-data :command-args [{"value" "bash" "label" "bash"}])
                                          :command "bash"
                                          :connection-method connection-method
                                          :secrets-manager-provider secrets-manager-provider)))
     (assoc db :connection-setup {:ssh-auth-method "password"
                                  :command-args [{"value" "bash" "label" "bash"}]
                                  :command "bash"
                                  :connection-method "manual-input"}))))

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
