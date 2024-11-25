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
 (fn [_ [_ handler query-params & params]]
   {:navigate {:handler handler
               :params params
               :query-params (or query-params {})}}))


(rf/reg-event-fx
 ::set-active-panel
 (fn [{:keys [db]} [_ active-panel]]
   (js/window.Intercom "update")
   {:db (assoc db :active-panel active-panel)}))

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
 :open-dialog
 (fn [{:keys [db]} [_ data]]
   {:db (assoc db
               :dialog-status :open
               :dialog-on-success (:on-success data)
               :dialog-text (:text data)
               :dialog-title (:title data))}))

(rf/reg-event-fx
 :hide-snackbar
 (fn
   [{:keys [db]} [_ _]]
   {:db (assoc db
               :snackbar-status :hidden
               :snackbar-level nil
               :snackbar-text nil)}))

(rf/reg-event-fx
 :show-snackbar
 (fn
   [{:keys [db]} [_ data]]
   {:db (assoc db
               :snackbar-status :shown
               :snackbar-level (:level data)
               :snackbar-text (:text data))}))

(rf/reg-event-fx
 :initialize-intercom
 (fn
   [{:keys [db]} [_ user]]
   (js/window.Intercom
    "boot"
    (clj->js {:api_base "https://api-iam.intercom.io"
              :app_id "ryuapdmp"
              :name (:name user)
              :email (:email user)
              :user_id (:email user)
              :user_hash (:intercom_hmac_digest user)}))
   {}))
