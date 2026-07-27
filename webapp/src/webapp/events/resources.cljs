(ns webapp.events.resources
  (:require
   [re-frame.core :as rf]
   [webapp.resources.federation.events]))

(rf/reg-event-fx
 :resources->create-resource
 (fn [{:keys [db]} [_ payload]]
   {:db (assoc-in db [:resources :creating?] true)
    :fx [[:dispatch [:fetch {:method "POST"
                             :uri "/resources"
                             :body payload
                             :on-success (fn [response]
                                           (rf/dispatch [:resources->create-success response]))
                             :on-failure (fn [error]
                                           (rf/dispatch [:resources->create-failure error]))}]]]}))

(rf/reg-event-fx
 :resources->create-success
 (fn [{:keys [db]} [_ resource]]
   (let [roles (get-in db [:resource-setup :roles] [])
         federation-roles (filter #(= (:connection-method %) "iam_federation") roles)]
     (if (empty? federation-roles)
       {:db (-> db
                (assoc-in [:resources :creating?] false)
                (assoc-in [:resources :last-created] resource))
        :fx [[:dispatch [:show-snackbar {:level :success
                                         :text "Resource created successfully!"}]]
             [:dispatch [:resource-setup->next-step :success]]]}
       ;; The resource + connections already exist here. Save each federation
       ;; config and finish only on full success; any failure rolls the whole
       ;; resource back so a retry starts clean.
       {:db (-> db
                (assoc-in [:resources :last-created] resource)
                (assoc-in [:resources :pending-federation-saves] (count federation-roles)))
        :fx (mapv (fn [role]
                    [:dispatch [:federation/save-for-new-role (:name role)]])
                  federation-roles)}))))

(rf/reg-event-fx
 :resources->create-failure
 (fn [{:keys [db]} [_ error]]
   {:db (assoc-in db [:resources :creating?] false)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to create resource"
                                     :details error}]]]}))

(rf/reg-event-fx
 :resources->federation-roles-saved
 (fn [{:keys [db]} _]
   (let [pending (dec (get-in db [:resources :pending-federation-saves] 1))]
     (if (pos? pending)
       {:db (assoc-in db [:resources :pending-federation-saves] pending)}
       {:db (-> db
                (assoc-in [:resources :creating?] false)
                (update :resources dissoc :pending-federation-saves))
        :fx [[:dispatch [:show-snackbar {:level :success
                                         :text "Resource created successfully!"}]]
             [:dispatch [:resource-setup->next-step :success]]]}))))

(rf/reg-event-fx
 :resources->federation-rollback
 (fn [{:keys [db]} [_ _connection-names error]]
   ;; Guard against duplicate rollbacks when multiple federation roles fail.
   (if (nil? (get-in db [:resources :pending-federation-saves]))
     {}
     (let [resource-name (get-in db [:resources :last-created :name])
           connection-names (mapv :name (get-in db [:resource-setup :roles] []))]
       {:db (-> db
                (assoc-in [:resources :creating?] false)
                (update :resources dissoc :pending-federation-saves))
        :fx [[:dispatch [:show-snackbar
                         {:level :error
                          :text "Resource creation failed: IAM Federation setup was rejected. Rolling back."
                          :details error}]]
             [:dispatch [:resources->rollback-create connection-names resource-name]]]}))))

;; Rollback for a failed federation create: the backend refuses to delete a
;; resource that still has connections, so we delete every connection first
;; and only then the resource itself.
(rf/reg-event-fx
 :resources->rollback-create
 (fn [{:keys [db]} [_ connection-names resource-name]]
   (if (empty? connection-names)
     {:fx [[:dispatch [:resources->rollback-resource resource-name]]]}
     {:db (assoc-in db [:resources :rollback-pending] (count connection-names))
      :fx (mapv (fn [name]
                  [:dispatch [:fetch {:method "DELETE"
                                      :uri (str "/connections/" name)
                                      :on-success (fn [_] (rf/dispatch [:resources->rollback-connection-done resource-name]))
                                      :on-failure (fn [_] (rf/dispatch [:resources->rollback-connection-done resource-name]))}]])
                connection-names)})))

(rf/reg-event-fx
 :resources->rollback-connection-done
 (fn [{:keys [db]} [_ resource-name]]
   (let [pending (dec (get-in db [:resources :rollback-pending] 1))]
     (if (pos? pending)
       {:db (assoc-in db [:resources :rollback-pending] pending)}
       {:db (update db :resources dissoc :rollback-pending)
        :fx [[:dispatch [:resources->rollback-resource resource-name]]]}))))

(rf/reg-event-fx
 :resources->rollback-resource
 (fn [{:keys [db]} [_ resource-name]]
   {:db (update db :resources dissoc :last-created)
    :fx (when resource-name
          [[:dispatch [:fetch {:method "DELETE"
                               :uri (str "/resources/" resource-name)
                               :on-success (fn [_] nil)
                               :on-failure (fn [_] nil)}]]])}))
