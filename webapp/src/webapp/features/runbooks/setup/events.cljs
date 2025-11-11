(ns webapp.features.runbooks.setup.events
  (:require
   [re-frame.core :as rf]))

(defn normalize-path
  "Remove leading slash from path if present"
  [path]
  (if (and (string? path) (seq path) (= (first path) "/"))
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

;; Runbooks Rules Events
(rf/reg-event-fx
 :runbooks-rules/get-all
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:runbooks-rules :list :status] :loading)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri "/runbooks/rules"
                             :on-success #(rf/dispatch [:runbooks-rules/get-all-success %])
                             :on-failure #(rf/dispatch [:runbooks-rules/get-all-failure %])}]]]}))

(rf/reg-event-db
 :runbooks-rules/get-all-success
 (fn [db [_ data]]
   (update-in db [:runbooks-rules :list] merge {:status :success :data data})))

(rf/reg-event-db
 :runbooks-rules/get-all-failure
 (fn [db [_ error]]
   (update-in db [:runbooks-rules :list] merge {:status :error :error error})))

(rf/reg-event-fx
 :runbooks-rules/get-by-id
 (fn [{:keys [db]} [_ rule-id]]
   {:db (assoc-in db [:runbooks-rules :active-rule :status] :loading)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/runbooks/rules/" rule-id)
                             :on-success #(rf/dispatch [:runbooks-rules/get-by-id-success %])
                             :on-failure #(rf/dispatch [:runbooks-rules/get-by-id-failure %])}]]]}))

(rf/reg-event-db
 :runbooks-rules/get-by-id-success
 (fn [db [_ data]]
   (update-in db [:runbooks-rules :active-rule] merge {:status :success :data data})))

(rf/reg-event-db
 :runbooks-rules/get-by-id-failure
 (fn [db [_ error]]
   (update-in db [:runbooks-rules :active-rule] merge {:status :error :error error})))

(rf/reg-event-fx
 :runbooks-rules/create
 (fn [{:keys [db]} [_ rule-data]]
   {:db db
    :fx [[:dispatch [:fetch {:method "POST"
                             :uri "/runbooks/rules"
                             :body rule-data
                             :on-success #(do
                                            (rf/dispatch [:runbooks-rules/create-success %])
                                            (rf/dispatch [:runbooks-rules/get-all]))
                             :on-failure #(rf/dispatch [:runbooks-rules/create-failure %])}]]]}))

(rf/reg-event-db
 :runbooks-rules/create-success
 (fn [db [_ _data]]
   db))

(rf/reg-event-db
 :runbooks-rules/create-failure
 (fn [db [_ error]]
   (update-in db [:runbooks-rules :list] merge {:status :error :error error})))

(rf/reg-event-fx
 :runbooks-rules/update
 (fn [{:keys [db]} [_ rule-id rule-data]]
   {:db db
    :fx [[:dispatch [:fetch {:method "PUT"
                             :uri (str "/runbooks/rules/" rule-id)
                             :body rule-data
                             :on-success #(do
                                            (rf/dispatch [:runbooks-rules/update-success %])
                                            (rf/dispatch [:runbooks-rules/get-all]))
                             :on-failure #(rf/dispatch [:runbooks-rules/update-failure %])}]]]}))

(rf/reg-event-db
 :runbooks-rules/update-success
 (fn [db [_ _data]]
   db))

(rf/reg-event-db
 :runbooks-rules/update-failure
 (fn [db [_ error]]
   (update-in db [:runbooks-rules :list] merge {:status :error :error error})))

(rf/reg-event-db
 :runbooks-rules/clear-active-rule
 (fn [db]
   (assoc-in db [:runbooks-rules :active-rule] {:status :idle :data nil :error nil})))
