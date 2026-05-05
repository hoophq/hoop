(ns webapp.provisioning.events
  (:require [re-frame.core :as rf]))

(defn- derive-stage [env]
  (if (get env :envvar:ADMIN_ACCOUNT)
    :needs-roles
    :needs-admin))

(defn- decode-env
  "Decodes a base64-encoded envvar value. Returns empty string on failure."
  [env-map key]
  (when-let [v (get env-map (keyword (str "envvar:" key)))]
    (try (js/atob v) (catch js/Error _ ""))))

(def ^:private subtype->display
  {"postgres" "PostgreSQL"})

(defn- api-resource->provisioning-resource
  [resource]
  (prn :kk resource)
  (let [env     (:env_vars resource)
        host    (or (decode-env env "HOST") "")
        port    (or (decode-env env "PORT") "")
        subtype (or (:subtype resource) (:type resource))]
    {:id       (:id resource)
     :name     (:name resource)
     :db-type  (get subtype->display subtype subtype)
     :address  (if (seq port) (str host ":" port) host)
     :host     host
     :port     port
     :agent-id (:agent_id resource)
     :admin    (decode-env env "ADMIN_ACCOUNT")
     :stage    (derive-stage env)
     :roles    []}))

(defn- compute-stage
  [resource]
  (cond
    (not (:admin resource)) (assoc resource :stage :needs-admin)
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

(defn- resource-catalog? [resource]
  (some? (get (:env_vars resource) :envvar:RESOURCE_CATALOG)))

(rf/reg-event-fx
 :provisioning/set-resources
 (fn [{:keys [db]} [_ api-resources]]
   (let [catalog-only (filterv resource-catalog? api-resources)
         api-mapped   (mapv (comp compute-stage api-resource->provisioning-resource) catalog-only)]
     {:db (-> db
              (assoc-in [:provisioning :resources :status] :ready)
              (assoc-in [:provisioning :resources :data] api-mapped))
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
   (update-in db [:provisioning :resources :data] into new-resources)))

(def ^:private display->subtype
  {"PostgreSQL" "postgres"
   "postgres"   "postgres"})

(defn- row->resource-request
  "Transforms a classified CSV row into the ResourceRequest body for POST /resources.
   Keys are prefixed with envvar: and values are base64-encoded, matching the gateway convention."
  [row]
  (let [subtype (get display->subtype (:db-type row) (:db-type row))]
    {:name     (:name row)
     :type     "database"
     :subtype  subtype
     :env_vars (cond-> {"envvar:RESOURCE_CATALOG" (js/btoa "true")}
                 (seq (:host row)) (assoc "envvar:HOST" (js/btoa (:host row)))
                 (seq (:port row)) (assoc "envvar:PORT" (js/btoa (str (:port row)))))}))

(rf/reg-event-fx
 :provisioning/import-resource
 (fn [_ [_ {:keys [row on-success on-failure]}]]
   (let [update? (= "update" (:status row))
         method  (if update? "PUT" "POST")
         uri     (if update? (str "/resources/" (:name row)) "/resources")]
     {:fx [[:dispatch [:fetch {:method     method
                               :uri        uri
                               :body       (row->resource-request row)
                               :on-success (fn [response]
                                             (on-success row response))
                               :on-failure (fn [error]
                                             (on-failure row error))}]]]})))

(rf/reg-event-fx
 :provisioning/import-next-resource
 (fn [_ [_ {:keys [queue index results on-progress on-complete]}]]
   (if (>= index (count queue))
     (do (on-complete results) {})
     (let [row (nth queue index)]
       {:fx [[:dispatch
              [:provisioning/import-resource
               {:row        row
                :on-success (fn [row response]
                              (on-progress (inc index) (count queue))
                              (rf/dispatch
                               [:provisioning/import-next-resource
                                {:queue       queue
                                 :index       (inc index)
                                 :results     (conj results {:row row :status :success :response response})
                                 :on-progress on-progress
                                 :on-complete on-complete}]))
                :on-failure (fn [row error]
                              (on-progress (inc index) (count queue))
                              (rf/dispatch
                               [:provisioning/import-next-resource
                                {:queue       queue
                                 :index       (inc index)
                                 :results     (conj results {:row row :status :failed :error error})
                                 :on-progress on-progress
                                 :on-complete on-complete}]))}]]]}))))

(rf/reg-event-fx
 :provisioning/set-admin-credentials
 (fn [_ [_ {:keys [resource-name username password agent-id on-success on-failure]}]]
   {:fx [[:dispatch [:fetch {:method "GET"
                             :uri    (str "/resources/" resource-name)
                             :on-success
                             (fn [resource]
                               (let [existing-envs (or (:env_vars resource) {})
                                     merged-envs   (merge (js->clj existing-envs)
                                                          {"envvar:USER" (js/btoa username)
                                                           "envvar:PASS" (js/btoa password)
                                                           "envvar:ADMIN_ACCOUNT" (js/btoa username)})
                                     body          {:name     (:name resource)
                                                    :type     (:type resource)
                                                    :subtype  (or (:subtype resource) (:type resource))
                                                    :agent_id (or agent-id (:agent_id resource) "")
                                                    :env_vars merged-envs}]
                                 (rf/dispatch [:fetch {:method     "PUT"
                                                       :uri        (str "/resources/" resource-name)
                                                       :body       body
                                                       :on-success (fn [resp] (on-success resource-name resp))
                                                       :on-failure (fn [err] (on-failure resource-name err))}])))
                             :on-failure (fn [err] (on-failure resource-name err))}]]]}))

(rf/reg-event-fx
 :provisioning/apply-admin-next
 (fn [_ [_ {:keys [queue index results agent-id on-progress on-complete]}]]
   (if (>= index (count queue))
     (do (on-complete results) {})
     (let [{:keys [resource-name username password]} (nth queue index)]
       {:fx [[:dispatch
              [:provisioning/set-admin-credentials
               {:resource-name resource-name
                :username      username
                :password      password
                :agent-id      agent-id
                :on-success    (fn [name _resp]
                                 (on-progress (inc index) (count queue))
                                 (rf/dispatch
                                  [:provisioning/apply-admin-next
                                   {:queue       queue
                                    :index       (inc index)
                                    :results     (conj results {:name name :status :success})
                                    :agent-id    agent-id
                                    :on-progress on-progress
                                    :on-complete on-complete}]))
                :on-failure    (fn [name err]
                                 (on-progress (inc index) (count queue))
                                 (rf/dispatch
                                  [:provisioning/apply-admin-next
                                   {:queue       queue
                                    :index       (inc index)
                                    :results     (conj results {:name name :status :failed :error err})
                                    :agent-id    agent-id
                                    :on-progress on-progress
                                    :on-complete on-complete}]))}]]]}))))

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
