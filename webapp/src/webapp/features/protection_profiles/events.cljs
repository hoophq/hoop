(ns webapp.features.protection-profiles.events
  (:require [re-frame.core :as rf]))

;; Fetches the organization's active protection profile (AdminOnly endpoint).
;; Consumers render nothing when the fetch fails or no profile is active, so
;; failures are stored silently — no snackbar.
(rf/reg-event-fx
 :protection-profile/fetch
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:protection-profile :status] :loading)
    :fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/orgs/protection-profile"
                      :on-success #(rf/dispatch [:protection-profile/fetch-success %])
                      :on-failure #(rf/dispatch [:protection-profile/fetch-failure %])}]]]}))

(rf/reg-event-db
 :protection-profile/fetch-success
 (fn [db [_ response]]
   (-> db
       (assoc-in [:protection-profile :status] :success)
       (assoc-in [:protection-profile :active]
                 {:profile (:profile response)
                  :attribute-name (:attribute_name response)}))))

(rf/reg-event-db
 :protection-profile/fetch-failure
 (fn [db [_ _error]]
   (-> db
       (assoc-in [:protection-profile :status] :error)
       (assoc-in [:protection-profile :active] nil))))
