(ns webapp.events
  (:require
   [re-frame.core :as rf]
   [webapp.http.api :as api]
   [webapp.http.request :as request]
   [webapp.db :as db]))

(rf/reg-event-db
 ::initialize-db
 (fn [_ _]
   db/default-db))

(rf/reg-event-fx
 :navigate
 (fn [{:keys [db]} [_ handler query-params & params]]
   {:db (assoc db :navigation-status :transitioning)
    :navigate {:handler handler
               :params params
               :query-params (or query-params {})}}))


(rf/reg-event-fx
 ::set-active-panel
 (fn [{:keys [db]} [_ active-panel]]
   (js/window.Intercom "update")
   {:db (-> db
            (assoc :active-panel active-panel)
            (assoc :navigation-status :completed))}))

(rf/reg-event-fx
 :fetch
 (fn
   [_ [_ request-info]]
   (api/request request-info)
   {}))

(rf/reg-event-fx
 :http-request
 (fn
   [{:keys [db]} [_ request-info]]
   (request/request request-info)
   {}))

(rf/reg-event-fx
 :destroy-page-loader
 (fn
   [{:keys [db]} [_ _]]
   {:db (assoc db :page-loader-status :closed)}))

;; Webclient active panel events
(rf/reg-event-fx
 :webclient/set-active-panel
 (fn [{:keys [db]} [_ panel-type]]
   (let [current-panel (get db :webclient->active-panel)
         new-panel (when-not (= current-panel panel-type) panel-type)]
     {:db (assoc db :webclient->active-panel new-panel)})))

(rf/reg-event-fx
 :close-page-loader
 (fn
   [{:keys [db]} [_ _]]
   {:db (assoc db :page-loader-status :closing)}))

(rf/reg-event-fx
 :close-modal
 (fn
   [{:keys [db]} [_ _]]
   {:db (assoc-in db [:modal] {:status :closed
                               :component nil
                               :size :small
                               :aaaaa true
                               :on-click-out nil})}))

(rf/reg-event-fx
 :open-modal
 (fn
   [{:keys [db]} [_ component size on-click-out loading]]
   {:db (assoc-in db [:modal] {:status (if loading :loading :open)
                               :component component
                               :size (or size :small)
                               :on-click-out on-click-out})}))

(rf/reg-event-fx
 :clear-dialog
 (fn [{:keys [db]} [_ _]]
   {:db (assoc db
               :dialog-on-success nil
               :dialog-text nil
               :dialog-title nil)}))

(rf/reg-event-fx
 :close-dialog
 (fn [{:keys [db]} [_ _]]
   (js/setTimeout #(rf/dispatch [:clear-dialog]) 500)
   {:db (assoc db :dialog-status :closed)}))

(rf/reg-event-fx
 :initialize-intercom
 (fn
   [{:keys [db]} [_ user]]
   (let [analytics-tracking (= "enabled" (get-in db [:gateway->info :data :analytics_tracking] "disabled"))]
     (when js/window.Intercom
       (js/window.Intercom "shutdown"))

     (if (not analytics-tracking)
       ;; If analytics tracking is disabled, don't initialize Intercom
       {}
       ;; Otherwise, initialize Intercom
       (do
         (if (= (.-hostname js/location) "localhost")

           (js/window.Intercom
            "boot"
            (clj->js {:api_base "https://api-iam.intercom.io"
                      :app_id "ryuapdmp"
                      :hide_default_launcher true
                      :custom_launcher_selector "#intercom-support-trigger"}))

           (js/window.Intercom
            "boot"
            (clj->js {:api_base "https://api-iam.intercom.io"
                      :app_id "ryuapdmp"
                      :name (:name user)
                      :email (:email user)
                      :user_id (:email user)
                      :user_hash (:intercom_hmac_digest user)
                      :hide_default_launcher true
                      :custom_launcher_selector "#intercom-support-trigger"})))
         {})))))
