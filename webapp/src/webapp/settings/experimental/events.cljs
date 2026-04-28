(ns webapp.settings.experimental.events
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :settings-experimental/get-flags
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:settings-experimental :status] :loading)
    :fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/feature-flags"
                      :on-success #(rf/dispatch [:settings-experimental/get-flags-success %])
                      :on-failure #(rf/dispatch [:settings-experimental/get-flags-failure %])}]]]}))

(rf/reg-event-db
 :settings-experimental/get-flags-success
 (fn [db [_ flags]]
   (-> db
       (assoc-in [:settings-experimental :status] :success)
       (assoc-in [:settings-experimental :flags] (vec flags)))))

(rf/reg-event-fx
 :settings-experimental/get-flags-failure
 (fn [{:keys [db]} [_ err]]
   {:db (assoc-in db [:settings-experimental :status] :error)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to load feature flags"
                                     :details err}]]]}))

(defn- find-flag-idx [flags flag-name]
  (->> flags
       (keep-indexed #(when (= flag-name (:name %2)) %1))
       first))

(rf/reg-event-fx
 :settings-experimental/toggle
 (fn [{:keys [db]} [_ flag-name enabled?]]
   (let [flags (get-in db [:settings-experimental :flags])
         idx (find-flag-idx flags flag-name)]
     {:db (cond-> db
            idx (assoc-in [:settings-experimental :flags idx :enabled] enabled?)
            true (update-in [:settings-experimental :pending] (fnil conj #{}) flag-name))
      :fx [[:dispatch [:fetch
                       {:method "PUT"
                        :uri (str "/feature-flags/" flag-name)
                        :body {:enabled enabled?}
                        :on-success #(rf/dispatch [:settings-experimental/toggle-success flag-name %])
                        :on-failure #(rf/dispatch [:settings-experimental/toggle-failure flag-name (not enabled?) %])}]]]})))

(rf/reg-event-db
 :settings-experimental/toggle-success
 (fn [db [_ flag-name item]]
   (let [flags (get-in db [:settings-experimental :flags])
         idx (find-flag-idx flags flag-name)]
     (cond-> db
       idx (assoc-in [:settings-experimental :flags idx] item)
       true (update-in [:settings-experimental :pending] (fnil disj #{}) flag-name)))))

(rf/reg-event-fx
 :settings-experimental/toggle-failure
 (fn [{:keys [db]} [_ flag-name revert-to err]]
   (let [flags (get-in db [:settings-experimental :flags])
         idx (find-flag-idx flags flag-name)]
     {:db (cond-> db
            idx (assoc-in [:settings-experimental :flags idx :enabled] revert-to)
            true (update-in [:settings-experimental :pending] (fnil disj #{}) flag-name))
      :fx [[:dispatch [:show-snackbar {:level :error
                                       :text (str "Could not update " flag-name)
                                       :details err}]]]})))
