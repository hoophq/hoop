(ns webapp.features.access-control.subs
  (:require
   [re-frame.core :as rf]))

(rf/reg-sub
 :access-control/plugin
 (fn [db]
   (get-in db [:plugins->plugin-details :plugin])))

(rf/reg-sub
 :access-control/status
 (fn [db]
   (get-in db [:plugins->plugin-details :status])))

(rf/reg-sub
 :access-control/installed?
 :<- [:access-control/plugin]
 (fn [plugin]
   (or (:installed? plugin) false)))

(rf/reg-sub
 :access-control/connections
 :<- [:access-control/plugin]
 (fn [plugin]
   (:connections plugin)))

(rf/reg-sub
 :access-control/groups-with-permissions
 :<- [:access-control/connections]
 (fn [connections]
   (println "connections" connections)
   (when connections
     (reduce (fn [acc conn]
               (let [group-configs (or (:config conn) [])]
                 (reduce (fn [group-acc group-name]
                           (update group-acc group-name
                                   (fn [existing-conns]
                                     (conj (or existing-conns [])
                                           (select-keys conn [:id :name])))))
                         acc
                         group-configs)))
             {}
             connections))))

(rf/reg-sub
 :access-control/group-permissions
 :<- [:access-control/groups-with-permissions]
 (fn [groups-with-permissions [_ group-id]]
   (get groups-with-permissions group-id [])))

(rf/reg-sub
 :access-control/active-groups
 :<- [:access-control/groups-with-permissions]
 :<- [:user-groups]
 (fn [[groups-with-permissions user-groups]]
   (when (and groups-with-permissions user-groups)
     (map (fn [group]
            (assoc group :active? (contains? groups-with-permissions group)))
          user-groups))))
