(ns webapp.resources.federation.subs
  (:require
   [re-frame.core :as rf]))

(rf/reg-sub
 :federation/state
 (fn [db _]
   (get db :resources/federation)))

(rf/reg-sub
 :federation/status
 (fn [db _]
   (get-in db [:resources/federation :status])))

(rf/reg-sub
 :federation/data
 (fn [db _]
   (get-in db [:resources/federation :data])))

(rf/reg-sub
 :federation/form
 (fn [db _]
   (get-in db [:resources/federation :form])))

(rf/reg-sub
 :federation/credentials-editing?
 (fn [db _]
   (get-in db [:resources/federation :credentials-editing?])))

(rf/reg-sub
 :federation/mapping-editor-open?
 (fn [db _]
   (get-in db [:resources/federation :mapping-editor-open?])))

(rf/reg-sub
 :federation/save-status
 (fn [db _]
   (get-in db [:resources/federation :save-status])))

(rf/reg-sub
 :federation/test-status
 (fn [db _]
   (get-in db [:resources/federation :test-status])))

(rf/reg-sub
 :federation/test-result
 (fn [db _]
   (get-in db [:resources/federation :test-result])))
