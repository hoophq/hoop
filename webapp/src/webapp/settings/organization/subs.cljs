(ns webapp.settings.organization.subs
  (:require
   [re-frame.core :as rf]))

(rf/reg-sub
 :organization-settings
 (fn [db _]
   (get db :organization-settings)))

(rf/reg-sub
 :organization-settings->analytics-mode
 :<- [:organization-settings]
 (fn [settings _]
   (:analytics-mode settings)))

(rf/reg-sub
 :organization-settings->status
 :<- [:organization-settings]
 (fn [settings _]
   (:status settings)))

(rf/reg-sub
 :organization-settings->submitting?
 :<- [:organization-settings]
 (fn [settings _]
   (:submitting? settings)))
