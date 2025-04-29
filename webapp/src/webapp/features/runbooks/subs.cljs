(ns webapp.features.runbooks.subs
  (:require
   [re-frame.core :as rf]))

(rf/reg-sub
 :runbooks/plugin
 (fn [db]
   (get-in db [:plugins->plugin-details :plugin])))

(rf/reg-sub
 :runbooks/connections
 :<- [:runbooks/plugin]
 (fn [plugin]
   (:connections plugin)))

(rf/reg-sub
 :runbooks/paths-by-connection
 :<- [:runbooks/connections]
 (fn [connections]
   (when connections
     (reduce (fn [acc conn]
               (let [paths (or (:config conn) [])]
                 (assoc acc (:id conn) paths)))
             {}
             (or connections [])))))
