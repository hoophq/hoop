(ns webapp.features.runbooks.events
  (:require
   [re-frame.core :as rf]))

(defn normalize-path
  "Remove leading slash from path if present"
  [path]
  (if (and (string? path) (not (empty? path)) (= (first path) "/"))
    (subs path 1)
    path))

(rf/reg-event-fx
 :runbooks/add-path-to-connection
 (fn [{:keys [db]} [_ {:keys [path connection-id]}]]
   (let [normalized-path (normalize-path path)
         plugin (get-in db [:plugins->plugin-details :plugin])
         connections (or (:connections plugin) [])
         connection-exists? (some #(= (:id %) connection-id) connections)
         updated-connections (if connection-exists?
                               ;; Update existing connection
                               (map (fn [conn]
                                      (if (= (:id conn) connection-id)
                                        (if (or (nil? normalized-path) (empty? normalized-path))
                                          (assoc conn :config nil)
                                          (update conn :config (fn [_] [normalized-path])))
                                        conn))
                                    connections)
                               ;; Add new connection to existing list
                               (conj connections {:id connection-id
                                                  :config (if (or (nil? normalized-path) (empty? normalized-path))
                                                            nil
                                                            [normalized-path])}))
         updated-plugin (assoc plugin :connections (vec updated-connections))]
     {:fx [[:dispatch [:plugins->update-plugin updated-plugin]]]})))

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
