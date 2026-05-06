(ns webapp.provisioning.events
  (:require [clojure.string :as cs]
            [re-frame.core :as rf]))

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

(defn- resource-catalog? [resource]
  (some? (get (:env_vars resource) :envvar:RESOURCE_CATALOG)))

(defn- fetch-resources-page!
  "Fetches a single page. Accumulates results and recurses until all pages are fetched."
  [page acc]
  (rf/dispatch
   [:fetch {:method "GET"
            :uri    (str "/resources?page=" page "&page_size=100")
            :on-success
            (fn [resp]
              (let [data       (or (:data resp) [])
                    all        (into acc data)
                    total      (get-in resp [:pages :total] 0)
                    fetched    (count all)]
                (if (< fetched total)
                  (fetch-resources-page! (inc page) all)
                  (rf/dispatch [:provisioning/set-resources all]))))
            :on-failure #(rf/dispatch [:provisioning/set-resources-error %])}]))

(rf/reg-event-fx
 :provisioning/fetch-resources
 (fn [{:keys [db]} _]
   (fetch-resources-page! 1 [])
   {:db (assoc-in db [:provisioning :resources :status] :loading)}))

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

(defn- normalize-env-keys
  "Coerces env_var map keys to strings. The API response uses keyword keys
   like :envvar:HOST, but PUT/POST bodies must use string keys."
  [envs]
  (reduce-kv (fn [m k v]
               (assoc m (if (keyword? k) (name k) k) v))
             {}
             (or envs {})))

(rf/reg-event-fx
 :provisioning/import-resource
 (fn [_ [_ {:keys [row on-success on-failure]}]]
   (let [update? (= "update" (:status row))]
     (if update?
       ;; Fetch existing resource first so we can preserve admin credentials
       ;; (USER, PASS, ADMIN_ACCOUNT) and any other env vars set outside the CSV.
       {:fx [[:dispatch
              [:fetch {:method "GET"
                       :uri    (str "/resources/" (:name row))
                       :on-success
                       (fn [resource]
                         (let [subtype       (get display->subtype (:db-type row) (:db-type row))
                               existing-envs (normalize-env-keys (:env_vars resource))
                               new-envs      (cond-> {"envvar:RESOURCE_CATALOG" (js/btoa "true")}
                                               (seq (:host row)) (assoc "envvar:HOST" (js/btoa (:host row)))
                                               (seq (:port row)) (assoc "envvar:PORT" (js/btoa (str (:port row)))))
                               merged-envs   (merge existing-envs new-envs)
                               body          {:name     (:name resource)
                                              :type     (or (:type resource) "database")
                                              :subtype  (or (:subtype resource) subtype)
                                              :agent_id (or (:agent_id resource) "")
                                              :env_vars merged-envs}]
                           (rf/dispatch [:fetch {:method     "PUT"
                                                 :uri        (str "/resources/" (:name row))
                                                 :body       body
                                                 :on-success (fn [response] (on-success row response))
                                                 :on-failure (fn [error] (on-failure row error))}])))
                       :on-failure (fn [error] (on-failure row error))}]]]}
       {:fx [[:dispatch [:fetch {:method     "POST"
                                 :uri        "/resources"
                                 :body       (row->resource-request row)
                                 :on-success (fn [response] (on-success row response))
                                 :on-failure (fn [error] (on-failure row error))}]]]}))))

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

;; ── Role plan flow ───────────────────────────────────────────────────────────
;; POST /resources/{name}/plan for each role row, async with mock delays.

(defn- mock-plan-response
  "Generates a mock plan response. ~80 % Create, ~10 % Update, ~10 % Failed."
  [resource-name role]
  (let [r     (rand)
        status (cond (< r 0.1) "Failed" (< r 0.2) "Update" :else "Create")
        pid   (str "plan-" (.now js/Date) "-" (rand-int 10000))]
    {:plan-id    pid
     :status     status
     :session-id (str "sess-" pid)
     :resource-name resource-name
     :role       role}))

(rf/reg-event-db
 :provisioning/set-plan-job
 (fn [db [_ plan-job]]
   (assoc-in db [:provisioning :plan-job] plan-job)))

(rf/reg-event-db
 :provisioning/update-plan-item
 (fn [db [_ item-key update-fn]]
   (update-in db [:provisioning :plan-job :items]
              (fn [items]
                (mapv (fn [it]
                        (if (= (:key it) item-key)
                          (update-fn it)
                          it))
                      items)))))

(rf/reg-event-fx
 :provisioning/start-role-plans
 (fn [{:keys [db]} [_ {:keys [resources roles-by-resource]}]]
   (let [res-by-id (into {} (map (fn [r] [(:id r) r]) resources))
         items     (vec
                    (mapcat
                     (fn [[rid role-list]]
                       (let [r (get res-by-id rid)]
                         (map-indexed
                          (fn [idx role-entry]
                            {:key           (str rid "-" idx)
                             :resource-id   rid
                             :resource-name (:name r)
                             :role          (:role role-entry)
                             :database      (:database role-entry)
                             :permissions   (:permissions role-entry)
                             :status        "pending"
                             :plan-id       nil
                             :session-id    nil})
                          role-list)))
                     roles-by-resource))
         plan-job  {:id    (str "plan-" (.now js/Date))
                    :items items
                    :started-at (.now js/Date)}]
     {:db (assoc-in db [:provisioning :plan-job] plan-job)
      :fx (vec (map-indexed
                (fn [idx item]
                  [:dispatch-later
                   {:ms (+ 200 (* idx 100))
                    :dispatch [:provisioning/request-plan (:key item)]}])
                items))})))

(rf/reg-event-fx
 :provisioning/request-plan
 (fn [{:keys [db]} [_ item-key]]
   (let [item (some #(when (= (:key %) item-key) %)
                    (get-in db [:provisioning :plan-job :items]))]
     (when item
       ;; Mark as processing, then simulate async response
       {:db (update-in db [:provisioning :plan-job :items]
                       (fn [items]
                         (mapv (fn [it]
                                 (if (= (:key it) item-key)
                                   (assoc it :status "processing")
                                   it))
                               items)))
        :fx [[:dispatch-later
              {:ms       (+ 800 (rand-int 2000))
               :dispatch [:provisioning/plan-response
                          item-key
                          (mock-plan-response (:resource-name item) (:role item))]}]]}))))

(rf/reg-event-db
 :provisioning/plan-response
 (fn [db [_ item-key response]]
   (update-in db [:provisioning :plan-job :items]
              (fn [items]
                (mapv (fn [it]
                        (if (= (:key it) item-key)
                          (assoc it
                                 :status     (:status response)
                                 :plan-id    (:plan-id response)
                                 :session-id (:session-id response))
                          it))
                      items)))))

(rf/reg-event-fx
 :provisioning/retry-plan
 (fn [_ [_ item-key]]
   {:fx [[:dispatch [:provisioning/request-plan item-key]]]}))

;; ── Apply flow ──────────────────────────────────────────────────────────────
;; POST /resources/{name}/apply — executes the planned change.

(defn- mock-apply-response
  "~90 % success, ~10 % failure."
  [item]
  (let [ok? (> (rand) 0.1)]
    {:status     (if ok? "Applied" "ApplyFailed")
     :session-id (str "sess-apply-" (.now js/Date) "-" (rand-int 10000))
     :plan-id    (:plan-id item)}))

(rf/reg-event-fx
 :provisioning/apply-plan
 (fn [{:keys [db]} [_ item-key]]
   (let [item (some #(when (= (:key %) item-key) %)
                    (get-in db [:provisioning :plan-job :items]))]
     (when item
       {:db (update-in db [:provisioning :plan-job :items]
                       (fn [items]
                         (mapv (fn [it]
                                 (if (= (:key it) item-key)
                                   (assoc it :status "applying")
                                   it))
                               items)))
        :fx [[:dispatch-later
              {:ms       (+ 600 (rand-int 1500))
               :dispatch [:provisioning/apply-response
                          item-key
                          (mock-apply-response item)]}]]}))))

(rf/reg-event-db
 :provisioning/apply-response
 (fn [db [_ item-key response]]
   (update-in db [:provisioning :plan-job :items]
              (fn [items]
                (mapv (fn [it]
                        (if (= (:key it) item-key)
                          (assoc it
                                 :status     (:status response)
                                 :session-id (:session-id response))
                          it))
                      items)))))

(rf/reg-event-fx
 :provisioning/apply-all
 (fn [{:keys [db]} _]
   (let [items (get-in db [:provisioning :plan-job :items])
         applicable (filterv #(contains? #{"Create" "Update"} (:status %)) items)]
     {:fx (vec (map-indexed
                (fn [idx item]
                  [:dispatch-later
                   {:ms (* idx 80)
                    :dispatch [:provisioning/apply-plan (:key item)]}])
                applicable))})))

(defn- mock-session-output
  "Generates realistic mock session output based on the item's current status."
  [item]
  (let [header  (str "-- Session for plan: " (:plan-id item) "\n"
                     "-- Resource: " (:resource-name item) "\n"
                     "-- Role: " (:role item) "\n"
                     "-- Database: " (:database item) "\n"
                     "-- Permissions: " (:permissions item) "\n\n")
        schema  (or (last (cs/split (or (:database item) "") #"\.")) "public")]
    (case (:status item)
      "Failed"
      (str header
           "ERROR: could not connect to server: Connection refused\n"
           "\tIs the server running on host \"" (:resource-name item) ".internal\"\n"
           "\tand accepting TCP/IP connections on port 5432?\n\n"
           "-- ✗ Plan failed after 30.0s (connection timeout)")

      "Create"
      (str header
           "-- DRY RUN: the following statements WILL be executed on apply\n\n"
           "BEGIN;\n"
           "CREATE ROLE \"" (:role item) "\";\n"
           "GRANT CONNECT ON DATABASE \"" (:resource-name item) "\" TO \"" (:role item) "\";\n"
           "GRANT USAGE ON SCHEMA " schema " TO \"" (:role item) "\";\n"
           "GRANT " (:permissions item) " ON ALL TABLES IN SCHEMA " schema
           " TO \"" (:role item) "\";\n"
           "ALTER DEFAULT PRIVILEGES IN SCHEMA " schema "\n"
           "  GRANT " (:permissions item) " ON TABLES TO \"" (:role item) "\";\n"
           "COMMIT;\n\n"
           "-- ✓ Plan: Create — role does not exist, will be created")

      "Update"
      (str header
           "-- DRY RUN: the following statements WILL be executed on apply\n\n"
           "BEGIN;\n"
           "-- Role \"" (:role item) "\" already exists, updating grants\n"
           "REVOKE ALL ON ALL TABLES IN SCHEMA " schema " FROM \"" (:role item) "\";\n"
           "GRANT USAGE ON SCHEMA " schema " TO \"" (:role item) "\";\n"
           "GRANT " (:permissions item) " ON ALL TABLES IN SCHEMA " schema
           " TO \"" (:role item) "\";\n"
           "ALTER DEFAULT PRIVILEGES IN SCHEMA " schema "\n"
           "  GRANT " (:permissions item) " ON TABLES TO \"" (:role item) "\";\n"
           "COMMIT;\n\n"
           "-- ✓ Plan: Update — role exists, grants will be refreshed")

      "Applied"
      (str header
           "BEGIN;\n"
           "CREATE ROLE \"" (:role item) "\";\n"
           "GRANT CONNECT ON DATABASE \"" (:resource-name item) "\" TO \"" (:role item) "\";\n"
           "GRANT USAGE ON SCHEMA " schema " TO \"" (:role item) "\";\n"
           "GRANT " (:permissions item) " ON ALL TABLES IN SCHEMA " schema
           " TO \"" (:role item) "\";\n"
           "ALTER DEFAULT PRIVILEGES IN SCHEMA " schema "\n"
           "  GRANT " (:permissions item) " ON TABLES TO \"" (:role item) "\";\n"
           "COMMIT;\n\n"
           "-- ✓ Applied successfully in 1.2s")

      "ApplyFailed"
      (str header
           "BEGIN;\n"
           "CREATE ROLE \"" (:role item) "\";\n"
           "ERROR: role \"" (:role item) "\" already exists\n"
           "ROLLBACK;\n\n"
           "-- ✗ Apply failed: duplicate role — resolve manually or retry with UPDATE strategy")

      ;; fallback
      (str header "-- Status: " (:status item)))))

(rf/reg-event-fx
 :provisioning/fetch-plan-session
 (fn [{:keys [db]} [_ session-id]]
   (let [item (some #(when (= (:session-id %) session-id) %)
                    (get-in db [:provisioning :plan-job :items]))]
     {:fx [[:dispatch-later
            {:ms 300
             :dispatch
             [:provisioning/add-sessions
              [{:id            session-id
                :job-id        (get-in db [:provisioning :plan-job :id])
                :resource-id   (:resource-id item)
                :resource-name (:resource-name item)
                :role-name     (:role item)
                :status        (if (contains? #{"Failed" "ApplyFailed"} (:status item))
                                 "error" "success")
                :started-at    (.now js/Date)
                :duration-ms   (+ 500 (rand-int 1000))
                :output        (mock-session-output item)}]]}]]})))
