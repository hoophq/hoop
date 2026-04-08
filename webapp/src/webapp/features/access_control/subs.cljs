(ns webapp.features.access-control.subs
  (:require
   [re-frame.core :as rf]))

(rf/reg-sub
 :access-control/plugin
 (fn [db]
   (get-in db [:plugins->plugin-details :plugin])))

(rf/reg-sub
 :access-control/connections
 :<- [:access-control/plugin]
 (fn [plugin]
   (:connections plugin)))

(rf/reg-sub
 :access-control/groups-with-permissions
 :<- [:access-control/connections]
 (fn [connections]
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
             (or connections [])))))

(rf/reg-sub
 :access-control/group-permissions
 :<- [:access-control/groups-with-permissions]
 (fn [groups-with-permissions [_ group-id]]
   (get groups-with-permissions group-id [])))

;; Subscription that merges groups from /users/groups with groups from the plugin
(rf/reg-sub
 :access-control/all-groups
 :<- [:user-groups-full]
 :<- [:access-control/connections]
 (fn [[user-groups-full connections]]
   (let [;; Build a map from group name -> label from the API response
         groups-map (reduce (fn [acc g]
                              (let [name (if (string? g) g (:name g))
                                    label (if (string? g) "" (or (:label g) ""))]
                                (assoc acc name label)))
                            {}
                            (or user-groups-full []))

         ;; Groups found in plugin connection configs (just names)
         plugin-groups (when connections
                         (->> connections
                              (mapcat #(or (:config %) []))
                              (into #{})))

         ;; Merge plugin groups that don't exist in API groups
         all-groups-map (reduce (fn [acc group-name]
                                  (if (contains? acc group-name)
                                    acc
                                    (assoc acc group-name "")))
                                groups-map
                                (or plugin-groups #{}))

         ;; Filter admin group, sort, and return as vector of maps
         filtered-groups (->> all-groups-map
                              (remove #(= (key %) "admin"))
                              (sort-by key)
                              (mapv (fn [[name label]] {:name name :label label})))]
     filtered-groups)))

;; Subscription to look up a single group's label by name
(rf/reg-sub
 :access-control/group-label
 :<- [:access-control/all-groups]
 (fn [all-groups [_ group-name]]
   (let [group (first (filter #(= (:name %) group-name) all-groups))]
     (or (:label group) ""))))
