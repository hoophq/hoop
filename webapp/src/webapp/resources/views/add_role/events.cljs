(ns webapp.resources.views.add-role.events
  (:require
   [re-frame.core :as rf]
   [webapp.resources.views.setup.events.process-form :as process-form]))

;; Initialize state for adding roles to existing resource
(rf/reg-event-fx
 :add-role->initialize
 (fn [{:keys [db]} [_ resource-id]]
   {:db (assoc-in db [:resource-setup :loading?] true)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/resources/" resource-id)
                             :on-success (fn [response]
                                           (rf/dispatch [:add-role->set-resource-data resource-id response]))}]]]}))

(rf/reg-event-db
 :add-role->set-resource-data
 (fn [db [_ resource-id resource-data]]
   (assoc db :resource-setup {:context :add-role
                              :resource-id resource-id
                              :name (:name resource-data)
                              :type (:type resource-data)
                              :subtype (:type resource-data) ;; Backend returns type as subtype
                              :agent-id (:agent_id resource-data)
                              :current-step :roles
                              :roles []
                              :loading? false})))

;; Submit - create roles individually via POST /connections
(rf/reg-event-fx
 :add-role->submit
 (fn [{:keys [db]} _]
   (let [resource-name (get-in db [:resource-setup :name])
         agent-id (get-in db [:resource-setup :agent-id])
         roles (get-in db [:resource-setup :roles] [])
         total-roles (count roles)]

     (if (empty? roles)
       {:fx [[:dispatch [:show-snackbar {:level :error
                                         :text "Please add at least one role"}]]]}

       {:db (assoc-in db [:resource-setup :submitting?] true)
        :fx (vec (map-indexed
                  (fn [idx role]
                    (let [processed-role (process-form/process-role role agent-id)
                          body (assoc processed-role :resource_name resource-name)]
                      [:dispatch [:fetch {:method "POST"
                                          :uri "/connections"
                                          :body body
                                          :on-success (fn [response]
                                                        (rf/dispatch [:add-role->role-created
                                                                      idx
                                                                      total-roles
                                                                      response]))
                                          :on-failure (fn [error]
                                                        (rf/dispatch [:add-role->role-failed
                                                                      idx
                                                                      total-roles
                                                                      (:name role)
                                                                      error]))}]]))
                  roles))}))))

;; Track successful role creation
(rf/reg-event-fx
 :add-role->role-created
 (fn [{:keys [db]} [_ _role-index total-roles response]]
   (let [created-roles (get-in db [:resource-setup :created-roles] [])
         new-created-roles (conj created-roles response)
         all-done? (= (count new-created-roles) total-roles)]

     {:db (assoc-in db [:resource-setup :created-roles] new-created-roles)
      :fx (if all-done?
            [[:dispatch [:add-role->all-roles-processed]]]
            [])})))

;; Track failed role creation
(rf/reg-event-fx
 :add-role->role-failed
 (fn [{:keys [db]} [_ _role-index total-roles role-name error]]
   (let [failed-roles (get-in db [:resource-setup :failed-roles] [])
         new-failed-roles (conj failed-roles {:name role-name :error error})
         created-count (count (get-in db [:resource-setup :created-roles] []))
         failed-count (count new-failed-roles)
         all-done? (= (+ created-count failed-count) total-roles)]

     {:db (assoc-in db [:resource-setup :failed-roles] new-failed-roles)
      :fx (concat
           [[:dispatch [:show-snackbar {:level :error
                                        :text (str "Failed to create role: " role-name)}]]]
           (if all-done?
             [[:dispatch [:add-role->all-roles-processed]]]
             []))})))

;; All roles processed (success or failure)
(rf/reg-event-fx
 :add-role->all-roles-processed
 (fn [{:keys [db]} _]
   (let [created-roles (get-in db [:resource-setup :created-roles] [])
         failed-roles (get-in db [:resource-setup :failed-roles] [])
         success-count (count created-roles)
         failed-count (count failed-roles)]

     {:db (-> db
              (assoc-in [:resource-setup :submitting?] false)
              (assoc-in [:resource-setup :current-step] :success)
              (assoc-in [:resources :last-created-roles] created-roles))
      :fx [[:dispatch [:show-snackbar {:level (if (zero? failed-count) :success :warning)
                                       :text (str success-count " role(s) created"
                                                  (when (pos? failed-count)
                                                    (str ", " failed-count " failed")))}]]
           [:dispatch [:plugins->get-my-plugins]]
           [:dispatch [:connections/get-connections-paginated {:force-refresh? true}]]]})))

;; Clear state when leaving
(rf/reg-event-db
 :add-role->clear
 (fn [db _]
   (dissoc db :resource-setup)))

