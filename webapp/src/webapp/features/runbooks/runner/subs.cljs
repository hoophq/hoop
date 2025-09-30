(ns webapp.features.runbooks.runner.subs
  (:require
   [clojure.string :as string]
   [re-frame.core :as rf]))

(rf/reg-sub
 :runbooks/connection-dialog-open?
 (fn [db]
   (get-in db [:runbooks :connection-dialog-open?])))

(rf/reg-sub
 :runbooks/execute-trigger
 (fn [db _]
   (get-in db [:runbooks :execute-trigger] false)))

(rf/reg-sub
 :runbooks/selected-connection
 (fn [db]
   (get-in db [:runbooks :selected-connection])))

(rf/reg-sub
 :runbooks/connections-status
 (fn [db]
   (get-in db [:runbooks :connections :status])))

(rf/reg-sub
 :runbooks/connections-list
 (fn [db]
   (get-in db [:runbooks :connections :list])))

(rf/reg-sub
 :runbooks/connections-error
 (fn [db]
   (get-in db [:runbooks :connections :error])))

(rf/reg-sub
 :runbooks/connection-filter
 (fn [db]
   (get-in db [:runbooks :connections :filter])))

(rf/reg-sub
 :runbooks/filtered-connections
 :<- [:runbooks/connections-list]
 :<- [:runbooks/connection-filter]
 (fn [[connections filter-text]]
   (if (empty? filter-text)
     connections
     (filter #(string/includes?
               (string/lower-case (:name %))
               (string/lower-case filter-text))
             connections))))