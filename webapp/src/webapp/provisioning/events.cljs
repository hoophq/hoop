(ns webapp.provisioning.events
  (:require [re-frame.core :as rf]
            [webapp.provisioning.data :as data]))

(defn- derive-stage [env]
  (if (get env "ADMIN_ACCOUNT")
    :needs-roles
    :needs-admin))

(defn- api-resource->provisioning-resource
  [resource]
  {:id            (:id resource)
   :name          (:name resource)
   :db-type       (or (:subtype resource) (:type resource))
   :host          (get (:env_vars resource) "HOST" "mock-for-now.localhost")
   :agent-id      (:agent_id resource)
   :admin         (get (:env_vars resource) "ADMIN_ACCOUNT" nil)
   :stage         (derive-stage (:env_vars resource))
   :roles         []})

(defn- compute-stage
  [resource]
  (cond
    (not (:admin-user resource)) (assoc resource :stage :needs-admin)
    (pos? (count (:roles resource))) (assoc resource :stage :ready
                                            :role-count (count (:roles resource)))
    :else (assoc resource :stage :needs-roles)))

(rf/reg-event-fx
 :provisioning/fetch-resources
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:provisioning :resources :status] :loading)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri "/resources"
                             :on-success #(rf/dispatch [:provisioning/set-resources %])
                             :on-failure #(rf/dispatch [:provisioning/set-resources-error %])}]]]}))

(rf/reg-event-fx
 :provisioning/set-resources
 (fn [{:keys [db]} [_ api-resources]]
   (let [api-mapped    (mapv (comp compute-stage api-resource->provisioning-resource) api-resources)
         mock          (vec data/initial-resources)
         existing-ids  (set (map :id api-mapped))
         mock-filtered (filterv #(not (existing-ids (:id %))) mock)
         merged        (into mock-filtered api-mapped)]
     {:db (-> db
              (assoc-in [:provisioning :resources :status] :ready)
              (assoc-in [:provisioning :resources :data] merged))
      :fx (mapv (fn [r]
                  [:dispatch [:provisioning/fetch-resource-roles (:name r)]])
                api-mapped)})))

(rf/reg-event-db
 :provisioning/set-resources-error
 (fn [db [_ _error]]
   (assoc-in db [:provisioning :resources :status] :error)))

(rf/reg-event-fx
 :provisioning/fetch-resource-roles
 (fn [_ [_ resource-name]]
   {:fx [[:dispatch [:fetch {:method "GET"
                             :uri "/connections"
                             :query-params {:resource_name resource-name}
                             :on-success #(rf/dispatch [:provisioning/set-resource-roles resource-name %])
                             :on-failure (fn [_])}]]]}))

(rf/reg-event-db
 :provisioning/set-resource-roles
 (fn [db [_ resource-name response]]
   (let [roles (get response :data response)
         role-list (if (sequential? roles) roles [])]
     (update-in db [:provisioning :resources :data]
                (fn [resources]
                  (mapv (fn [r]
                          (if (= (:name r) resource-name)
                            (compute-stage (assoc r :roles role-list
                                                  :role-count (count role-list)))
                            r))
                        resources))))))

(rf/reg-event-db
 :provisioning/add-resources
 (fn [db [_ new-resources]]
   (prn :new new-resources)
   (update-in db [:provisioning :resources :data] into new-resources)))

(rf/reg-event-db
 :provisioning/update-resources
 (fn [db [_ update-fn]]
   (update-in db [:provisioning :resources :data] update-fn)))

(rf/reg-event-db
 :provisioning/add-job
 (fn [db [_ job]]
   (update-in db [:provisioning :jobs] conj job)))

(rf/reg-event-db
 :provisioning/update-jobs
 (fn [db [_ update-fn]]
   (update-in db [:provisioning :jobs] update-fn)))

(rf/reg-event-db
 :provisioning/add-sessions
 (fn [db [_ new-sessions]]
   (update-in db [:provisioning :sessions] into new-sessions)))
