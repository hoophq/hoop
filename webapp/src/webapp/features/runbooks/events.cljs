(ns webapp.features.runbooks.events
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :runbooks/add-path-to-connection
 (fn [{:keys [db]} [_ {:keys [path connection-id]}]]
   (let [plugin (get-in db [:plugins->plugin-details :plugin])
         connections (or (:connections plugin) [])
         updated-connections (map (fn [conn]
                                    (if (= (:id conn) connection-id)
                                      (if (or (nil? path) (empty? path))
                                        (assoc conn :config nil)
                                        (update conn :config (fn [_] [path])))
                                      conn))
                                  connections)
         updated-plugin (assoc plugin :connections (vec updated-connections))]
     (if (empty? connections)
       {:fx [[:dispatch [:plugins->update-plugin
                         (assoc plugin :connections
                                [{:id connection-id
                                  :config (if (or (nil? path) (empty? path))
                                            nil
                                            [path])}])]]]}
       {:fx [[:dispatch [:plugins->update-plugin updated-plugin]]]}))))

(rf/reg-event-fx
 :runbooks/delete-path
 (fn [{:keys [db]} [_ path]]
   (let [plugin (get-in db [:plugins->plugin-details :plugin])
         connections (or (:connections plugin) [])
         updated-connections (map (fn [conn]
                                    (if (and (:config conn) (some #(= % path) (:config conn)))
                                      (update conn :config (fn [config]
                                                             (let [filtered (vec (remove #(= % path) config))]
                                                               (if (empty? filtered) nil filtered))))
                                      conn))
                                  connections)
         updated-plugin (assoc plugin :connections (vec updated-connections))]
     {:fx [[:dispatch [:plugins->update-plugin updated-plugin]]]})))
