(ns webapp.features.rulepacks.events
  (:require
   [clojure.string :as str]
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :rulepacks/list
 (fn [{:keys [db]} [_ {:keys [search]}]]
   (let [q (some-> search str/trim)
         uri (if (seq q)
               (str "/rulepacks?search=" (js/encodeURIComponent q))
               "/rulepacks")]
     {:db (-> db
              (assoc-in [:rulepacks :list :status] :loading)
              (assoc-in [:rulepacks :list :search] (or q "")))
      :fx [[:dispatch [:fetch {:method "GET"
                               :uri uri
                               :on-success #(rf/dispatch [:rulepacks/list-success %])
                               :on-failure #(rf/dispatch [:rulepacks/list-failure %])}]]]})))

(rf/reg-event-db
 :rulepacks/list-success
 (fn [db [_ response]]
   (-> db
       (assoc-in [:rulepacks :list :status] :success)
       (assoc-in [:rulepacks :list :data] (or (:data response) [])))))

(rf/reg-event-fx
 :rulepacks/list-failure
 (fn [{:keys [db]} [_ err]]
   {:db (assoc-in db [:rulepacks :list :status] :error)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to load rulepacks"
                                     :details err}]]]}))

(rf/reg-event-fx
 :rulepacks/get
 (fn [{:keys [db]} [_ rulepack-id]]
   {:db (-> db
            (assoc-in [:rulepacks :active :status] :loading)
            (assoc-in [:rulepacks :active :data] nil)
            (assoc-in [:rulepacks :selected-connections] #{}))
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/rulepacks/" rulepack-id)
                             :on-success #(rf/dispatch [:rulepacks/get-success %])
                             :on-failure #(rf/dispatch [:rulepacks/get-failure %])}]]]}))

(rf/reg-event-db
 :rulepacks/get-success
 (fn [db [_ rulepack]]
   (-> db
       (assoc-in [:rulepacks :active :status] :success)
       (assoc-in [:rulepacks :active :data] rulepack)
       (assoc-in [:rulepacks :selected-connections]
                 (set (or (:connection_names rulepack) []))))))

(rf/reg-event-fx
 :rulepacks/get-failure
 (fn [{:keys [db]} [_ err]]
   {:db (assoc-in db [:rulepacks :active :status] :error)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to load rulepack"
                                     :details err}]]]}))

(rf/reg-event-db
 :rulepacks/toggle-connection
 (fn [db [_ connection-name]]
   (update-in db [:rulepacks :selected-connections]
              (fn [s]
                (if (contains? s connection-name)
                  (disj s connection-name)
                  (conj s connection-name))))))

(rf/reg-event-db
 :rulepacks/reset-selected-connections
 (fn [db _]
   (let [original (or (get-in db [:rulepacks :active :data :connection_names]) [])]
     (assoc-in db [:rulepacks :selected-connections] (set original)))))

(rf/reg-event-fx
 :rulepacks/apply-connections
 (fn [{:keys [db]} _]
   (let [rulepack-id (get-in db [:rulepacks :active :data :id])
         selected (vec (get-in db [:rulepacks :selected-connections]))]
     {:db (assoc-in db [:rulepacks :applying?] true)
      :fx [[:dispatch [:fetch {:method "POST"
                               :uri (str "/rulepacks/" rulepack-id "/apply")
                               :body {:connection_names selected}
                               :on-success #(rf/dispatch [:rulepacks/apply-success rulepack-id])
                               :on-failure #(rf/dispatch [:rulepacks/apply-failure %])}]]]})))

(rf/reg-event-fx
 :rulepacks/apply-success
 (fn [{:keys [db]} [_ rulepack-id]]
   {:db (assoc-in db [:rulepacks :applying?] false)
    :fx [[:dispatch [:show-snackbar {:level :success
                                     :text "Rulepack applied successfully"}]]
         [:dispatch [:rulepacks/get rulepack-id]]]}))

(rf/reg-event-fx
 :rulepacks/apply-failure
 (fn [{:keys [db]} [_ err]]
   (let [missing (:missing_names err)]
     {:db (assoc-in db [:rulepacks :applying?] false)
      :fx [[:dispatch [:show-snackbar {:level :error
                                       :text (if (seq missing)
                                               (str "Unknown connections: "
                                                    (str/join ", " missing))
                                               "Failed to apply rulepack")
                                       :details err}]]]})))

(rf/reg-event-fx
 :rulepacks/fetch-connections
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:connections->list :status] :loading)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri "/connections"
                             :on-success #(rf/dispatch [:rulepacks/fetch-connections-success %])
                             :on-failure #(rf/dispatch [:rulepacks/fetch-connections-failure %])}]]]}))

(rf/reg-event-db
 :rulepacks/fetch-connections-success
 (fn [db [_ response]]
   (-> db
       (assoc-in [:connections->list :status] :success)
       (assoc-in [:connections->list :data] (or response [])))))

(rf/reg-event-fx
 :rulepacks/fetch-connections-failure
 (fn [{:keys [db]} [_ err]]
   {:db (assoc-in db [:connections->list :status] :error)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to load connections"
                                     :details err}]]]}))
