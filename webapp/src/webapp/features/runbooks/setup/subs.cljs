(ns webapp.features.runbooks.setup.subs
  (:require
   [re-frame.core :as rf]))

;; Runbooks Rules Subscriptions
(rf/reg-sub
 :runbooks-rules/list
 (fn [db]
   (get-in db [:runbooks-rules :list])))

(rf/reg-sub
 :runbooks-rules/list-loading
 :<- [:runbooks-rules/list]
 (fn [list]
   (= (:status list) :loading)))

(rf/reg-sub
 :runbooks-rules/active-rule
 (fn [db]
   (get-in db [:runbooks-rules :active-rule])))

;; Runbooks Configuration Subscriptions
(rf/reg-sub
 :runbooks-configurations/data
 (fn [db]
   (get-in db [:runbooks-configurations])))

(rf/reg-sub
 :runbooks-configurations/data-loading
 :<- [:runbooks-configurations/data]
 (fn [data]
   (= (:status data) :loading)))

;; Runbooks List Subscriptions
(rf/reg-sub
 :runbooks/list
 (fn [db]
   (get-in db [:runbooks :list])))

;; Subscription for runner - transforms list data to runner format
(rf/reg-sub
 :runbooks/runner-data
 :<- [:runbooks/list]
 :<- [:runbooks/selected-connection]
 (fn [[list-data selected-connection]]
   (if (nil? list-data)
     {:status :idle :data {:repositories [] :items []} :error nil}
     (let [status (:status list-data)
           repositories (or (:data list-data) [])
           connection-name (when selected-connection (:name selected-connection))

           ;; Filter repositories by connection if one is selected
           ;; Note: Server already filters, but we keep this for client-side filtering if needed
           filtered-repositories (if (and connection-name (seq repositories))
                                   (mapv (fn [repo]
                                           (assoc repo
                                                  :items
                                                  (filterv (fn [item]
                                                             (some #(= connection-name %) (:connections item)))
                                                           (:items repo))))
                                         repositories)
                                   repositories)

           ;; Flatten items for backward compatibility
           all-items (mapcat :items filtered-repositories)]
       {:status status
        :data {:repositories filtered-repositories
               :items all-items}
        :error (:error list-data)}))))
