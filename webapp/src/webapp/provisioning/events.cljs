(ns webapp.provisioning.events
  (:require [clojure.string :as cs]
            [re-frame.core :as rf]))

;; ── Pure helpers ────────────────────────────────────────────────────────────

(defn- update-items-where
  "Walk `items`, applying `f` to each item where `pred` returns truthy.
   Used by every event that mutates :provisioning/plan-job/:items."
  [items pred f]
  (mapv #(if (pred %) (f %) %) items))

(defn- set-status-where
  "Convenience wrapper: set :status on each item matching pred."
  [items pred new-status]
  (update-items-where items pred #(assoc % :status new-status)))

;; ── Env-var encoding helpers ────────────────────────────────────────────────

(def ^:private envvar-prefix "envvar:")

(defn- envvar-key [k]
  (str envvar-prefix k))

(defn- decode-env
  "Decodes a base64-encoded envvar value. Returns empty string on failure."
  [env-map key]
  (when-let [v (get env-map (keyword (envvar-key key)))]
    (try (js/atob v) (catch js/Error _ ""))))

(defn- encode-envs
  "Builds a map of `envvar:KEY` → base64(value), skipping empty values.
   Accepts a map of {KEY string-value}."
  [m]
  (reduce-kv (fn [acc k v]
               (if (and v (or (not (string? v)) (seq v)))
                 (assoc acc (envvar-key k) (js/btoa (str v)))
                 acc))
             {}
             m))

(defn- normalize-env-keys
  "Coerces env_var map keys to strings. The API response uses keyword keys
   like :envvar:HOST, but PUT/POST bodies must use string keys."
  [envs]
  (reduce-kv (fn [m k v]
               (assoc m (if (keyword? k) (name k) k) v))
             {}
             (or envs {})))

;; ── Resource transforms ─────────────────────────────────────────────────────

(defn- derive-stage [env]
  (if (get env :envvar:ADMIN_ACCOUNT)
    :needs-roles
    :needs-admin))

(def ^:private subtype->display
  {"postgres" "PostgreSQL"})

(def ^:private display->subtype
  {"PostgreSQL" "postgres"
   "postgres"   "postgres"})

(defn- api-resource->provisioning-resource
  [resource]
  (let [env     (:env_vars resource)
        host    (or (decode-env env "HOST") "")
        port    (or (decode-env env "PORT") "")
        subtype (or (:subtype resource) (:type resource))
        roles   (or (:roles resource) [])]
    {:id         (:id resource)
     :name       (:name resource)
     :db-type    (get subtype->display subtype subtype)
     :address    (if (seq port) (str host ":" port) host)
     :host       host
     :port       port
     :agent-id   (:agent_id resource)
     :admin      (decode-env env "ADMIN_ACCOUNT")
     ;; Decoded only so the bulk-admin screen can pre-fill the field when
     ;; editing existing credentials. The value still travels base64-encoded
     ;; over the wire (see encode-envs).
     :password   (decode-env env "PASS")
     :stage      (derive-stage env)
     :role-count (count roles)
     :roles      roles}))

(defn- compute-stage
  [resource]
  (cond
    (not (:admin resource)) (assoc resource :stage :needs-admin)
    (pos? (:role-count resource)) (assoc resource :stage :ready)
    :else (assoc resource :stage :needs-roles)))

(defn- resource-catalog? [resource]
  (some? (get (:env_vars resource) :envvar:RESOURCE_CATALOG)))

;; ── Resource list fetch ─────────────────────────────────────────────────────

(defn- fetch-resources-page!
  "Fetches a single page. Accumulates results and recurses until all pages are fetched."
  [page acc]
  (rf/dispatch
   [:fetch {:method "GET"
            :uri    (str "/resources?page=" page "&page_size=100")
            :on-success
            (fn [resp]
              (let [data    (or (:data resp) [])
                    all     (into acc data)
                    total   (get-in resp [:pages :total] 0)
                    fetched (count all)]
                (if (< fetched total)
                  (fetch-resources-page! (inc page) all)
                  (rf/dispatch [:provisioning/set-resources all]))))
            :on-failure #(rf/dispatch [:provisioning/set-resources-error %])}]))

(rf/reg-event-fx
 :provisioning/fetch-resources
 (fn [{:keys [db]} _]
   (fetch-resources-page! 1 [])
   {:db (assoc-in db [:provisioning :resources :status] :loading)}))

(rf/reg-event-db
 :provisioning/set-resources
 (fn [db [_ api-resources]]
   (let [catalog-only (filterv resource-catalog? api-resources)
         api-mapped   (mapv (comp compute-stage api-resource->provisioning-resource) catalog-only)]
     (-> db
         (assoc-in [:provisioning :resources :status] :ready)
         (assoc-in [:provisioning :resources :data] api-mapped)))))

(rf/reg-event-db
 :provisioning/set-resources-error
 (fn [db [_ _error]]
   (assoc-in db [:provisioning :resources :status] :error)))

(rf/reg-event-db
 :provisioning/add-resources
 (fn [db [_ new-resources]]
   (update-in db [:provisioning :resources :data] into new-resources)))

;; ── Resource update flow (fetch → merge envs → PUT) ─────────────────────────

(defn- merge-and-put-resource!
  "Fetches /resources/{name}, merges `env-overrides` into env_vars, PUTs the result.
   `env-overrides` must already be string-keyed and base64-encoded.
   Calls `(on-success name response)` / `(on-failure name error)`."
  [{:keys [resource-name env-overrides agent-id default-subtype on-success on-failure]}]
  (rf/dispatch
   [:fetch {:method "GET"
            :uri    (str "/resources/" resource-name)
            :on-success
            (fn [resource]
              (let [existing-envs (normalize-env-keys (:env_vars resource))
                    merged-envs   (merge existing-envs env-overrides)
                    body          {:name     (:name resource)
                                   :type     (or (:type resource) "database")
                                   :subtype  (or (:subtype resource) default-subtype)
                                   :agent_id (or agent-id (:agent_id resource) "")
                                   :env_vars merged-envs}]
                (rf/dispatch
                 [:fetch {:method     "PUT"
                          :uri        (str "/resources/" resource-name)
                          :body       body
                          :on-success #(on-success resource-name %)
                          :on-failure #(on-failure resource-name %)}])))
            :on-failure #(on-failure resource-name %)}]))

(defn- row->resource-request
  "Transforms a classified CSV row into the ResourceRequest body for POST /resources.
   Keys are prefixed with envvar: and values are base64-encoded."
  [row]
  (let [subtype (get display->subtype (:db-type row) (:db-type row))]
    {:name     (:name row)
     :type     "database"
     :subtype  subtype
     :env_vars (encode-envs (cond-> {"RESOURCE_CATALOG" "true"}
                              (seq (:host row)) (assoc "HOST" (:host row))
                              (seq (:port row)) (assoc "PORT" (str (:port row)))))}))

(rf/reg-event-fx
 :provisioning/import-resource
 (fn [_ [_ {:keys [row on-success on-failure]}]]
   (let [update?  (= "update" (:status row))
         subtype  (get display->subtype (:db-type row) (:db-type row))
         env-over (encode-envs (cond-> {"RESOURCE_CATALOG" "true"}
                                 (seq (:host row)) (assoc "HOST" (:host row))
                                 (seq (:port row)) (assoc "PORT" (str (:port row)))))]
     (if update?
       (do
         (merge-and-put-resource!
          {:resource-name   (:name row)
           :env-overrides   env-over
           :default-subtype subtype
           :on-success      (fn [_name response] (on-success row response))
           :on-failure      (fn [_name error]    (on-failure row error))})
         {})
       {:fx [[:dispatch [:fetch {:method     "POST"
                                 :uri        "/resources"
                                 :body       (row->resource-request row)
                                 :on-success #(on-success row %)
                                 :on-failure #(on-failure row %)}]]]}))))

(rf/reg-event-fx
 :provisioning/set-admin-credentials
 (fn [_ [_ {:keys [resource-name username password agent-id on-success on-failure]}]]
   (merge-and-put-resource!
    {:resource-name resource-name
     :env-overrides (encode-envs {"USER"          username
                                  "PASS"          password
                                  "ADMIN_ACCOUNT" username})
     :agent-id      agent-id
     :on-success    on-success
     :on-failure    on-failure})
   {}))

;; ── Generic async-queue dispatcher ──────────────────────────────────────────
;; Used by both import and admin-credential flows. Caller supplies a `step-fn`
;; that, given a queue item plus continuation callbacks, kicks off one async unit.

(rf/reg-event-fx
 :provisioning/run-queue-next
 (fn [_ [_ {:keys [queue index results step-fn on-progress on-complete] :as ctx}]]
   (cond
     (>= index (count queue))
     (do (on-complete results) {})

     :else
     (let [item       (nth queue index)
           continue!  (fn [result]
                        (on-progress (inc index) (count queue))
                        (rf/dispatch
                         [:provisioning/run-queue-next
                          (assoc ctx
                                 :index (inc index)
                                 :results (conj results result))]))]
       (step-fn item continue!)
       {}))))

(rf/reg-event-fx
 :provisioning/import-next-resource
 (fn [_ [_ {:keys [queue on-progress on-complete]}]]
   {:fx [[:dispatch
          [:provisioning/run-queue-next
           {:queue       queue
            :index       0
            :results     []
            :on-progress on-progress
            :on-complete on-complete
            :step-fn
            (fn [row continue!]
              (rf/dispatch
               [:provisioning/import-resource
                {:row        row
                 :on-success #(continue! {:row %1 :status :success :response %2})
                 :on-failure #(continue! {:row %1 :status :failed  :error    %2})}]))}]]]}))

(rf/reg-event-fx
 :provisioning/apply-admin-next
 (fn [_ [_ {:keys [queue agent-id on-progress on-complete]}]]
   {:fx [[:dispatch
          [:provisioning/run-queue-next
           {:queue       queue
            :index       0
            :results     []
            :on-progress on-progress
            :on-complete on-complete
            :step-fn
            (fn [{:keys [resource-name username password]} continue!]
              (rf/dispatch
               [:provisioning/set-admin-credentials
                {:resource-name resource-name
                 :username      username
                 :password      password
                 :agent-id      agent-id
                 :on-success    (fn [name _resp] (continue! {:name name :status :success}))
                 :on-failure    (fn [name err]   (continue! {:name name :status :failed :error err}))}]))}]]]}))

;; ── DB-update events ────────────────────────────────────────────────────────

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

;; ── Plan-job item helpers ───────────────────────────────────────────────────

(def ^:private plan-items-path [:provisioning :plan-job :items])

(defn- update-plan-items
  "Apply a transform to plan-job items in db."
  [db f]
  (update-in db plan-items-path f))

(defn- key= [k] #(= k (:key %)))

(rf/reg-event-db
 :provisioning/set-plan-job
 (fn [db [_ plan-job]]
   (assoc-in db [:provisioning :plan-job] plan-job)))

(rf/reg-event-db
 :provisioning/update-plan-item
 (fn [db [_ item-key update-fn]]
   (update-plan-items db #(update-items-where % (key= item-key) update-fn))))

;; ── Batch chunking helpers ──────────────────────────────────────────────────

(def ^:private plan-chunk-size 50)
(def ^:private apply-chunk-size 50)

(defn- split-permissions [perms-str]
  (vec (.split (or perms-str "") #"\s*,\s*")))

;; ── Plan flow (batch) ───────────────────────────────────────────────────────

(rf/reg-event-fx
 :provisioning/start-role-plans
 (fn [{:keys [db]} [_ {:keys [resources roles-by-resource]}]]
   (let [res-by-id (into {} (map (juxt :id identity) resources))
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
         plan-job  {:id         (str "plan-" (.now js/Date))
                    :items      items
                    :cancelled? false
                    :planning?  true
                    :started-at (.now js/Date)}]
     {:db (assoc-in db [:provisioning :plan-job] plan-job)
      :fx [[:dispatch [:provisioning/plan-next-chunk 0]]]})))

(rf/reg-event-fx
 :provisioning/plan-next-chunk
 (fn [{:keys [db]} [_ chunk-idx]]
   (let [plan-job (get-in db [:provisioning :plan-job])]
     (if (or (not plan-job) (:cancelled? plan-job))
       {}
       (let [pending (filterv #(= "pending" (:status %)) (:items plan-job))
             chunk   (vec (take plan-chunk-size pending))]
         (if (seq chunk)
           (let [chunk-keys (set (map :key chunk))
                 payload    {:items (mapv (fn [it]
                                            {:resource_name (:resource-name it)
                                             :role          (:role it)
                                             :database      (:database it)
                                             :permissions   (split-permissions (:permissions it))})
                                          chunk)}]
             {:db (update-plan-items db #(set-status-where % (comp chunk-keys :key) "processing"))
              :fx [[:dispatch
                    [:fetch {:method     "POST"
                             :uri        "/resources/plan/batch"
                             :body       payload
                             :on-success #(rf/dispatch [:provisioning/plan-batch-response chunk-idx chunk %])
                             :on-failure #(rf/dispatch [:provisioning/plan-batch-error chunk])}]]]})
           {:db (assoc-in db [:provisioning :plan-job :planning?] false)}))))))

(rf/reg-event-fx
 :provisioning/plan-batch-response
 (fn [{:keys [db]} [_ chunk-idx _chunk resp]]
   (let [result-map (into {}
                          (map (fn [r] [(str (:resource_name r) "|" (:role r)) r])
                               (:results resp)))
         apply-result (fn [it]
                        (if-let [r (get result-map
                                        (str (:resource-name it) "|" (:role it)))]
                          (assoc it
                                 :status     (:status r)
                                 :plan-id    (:plan_id r)
                                 :session-id (:session_id r))
                          it))]
     {:db (update-plan-items db #(mapv apply-result %))
      :fx [[:dispatch [:provisioning/plan-next-chunk (inc chunk-idx)]]]})))

(rf/reg-event-fx
 :provisioning/plan-batch-error
 (fn [{:keys [db]} [_ chunk]]
   (let [chunk-keys (set (map :key chunk))]
     {:db (update-plan-items db #(set-status-where % (comp chunk-keys :key) "Failed"))
      :fx [[:dispatch [:provisioning/plan-next-chunk 0]]]})))

(rf/reg-event-db
 :provisioning/plan-response
 (fn [db [_ item-key response]]
   (update-plan-items db
                      #(update-items-where % (key= item-key)
                                           (fn [it]
                                             (assoc it
                                                    :status     (:status response)
                                                    :plan-id    (:plan-id response)
                                                    :session-id (:session-id response)))))))

;; Single-item retry still uses the individual endpoint
(rf/reg-event-fx
 :provisioning/retry-plan
 (fn [{:keys [db]} [_ item-key]]
   (when-let [item (some #(when (= (:key %) item-key) %)
                         (get-in db plan-items-path))]
     (let [payload {:role        (:role item)
                    :database    (:database item)
                    :permissions (split-permissions (:permissions item))}]
       {:db (update-plan-items db #(set-status-where % (key= item-key) "processing"))
        :fx [[:dispatch
              [:fetch {:method     "POST"
                       :uri        (str "/resources/" (:resource-name item) "/plan")
                       :body       payload
                       :on-success #(rf/dispatch
                                     [:provisioning/plan-response item-key
                                      {:plan-id    (:plan_id %)
                                       :status     (:status %)
                                       :session-id (:session_id %)}])
                       :on-failure #(rf/dispatch
                                     [:provisioning/plan-response item-key
                                      {:plan-id nil :status "Failed" :session-id nil}])}]]]}))))

;; ── Cancel flow ─────────────────────────────────────────────────────────────

(rf/reg-event-db
 :provisioning/cancel-plan
 (fn [db _]
   (-> db
       (assoc-in [:provisioning :plan-job :cancelled?] true)
       (assoc-in [:provisioning :plan-job :planning?] false)
       (update-plan-items #(set-status-where % (comp #{"pending"} :status) "Cancelled")))))

(rf/reg-event-db
 :provisioning/cancel-apply
 (fn [db _]
   (-> db
       (assoc-in [:provisioning :plan-job :apply-cancelled?] true)
       (assoc-in [:provisioning :plan-job :applying?] false)
       (update-plan-items #(set-status-where % (comp #{"Create" "Update"} :status) "Cancelled")))))

(rf/reg-event-db
 :provisioning/cancel-plan-item
 (fn [db [_ item-key]]
   (let [cancellable? (fn [it]
                        (and (= (:key it) item-key)
                             (contains? #{"pending" "Create" "Update"} (:status it))))]
     (update-plan-items db #(set-status-where % cancellable? "Cancelled")))))

;; ── Apply flow (batch) ──────────────────────────────────────────────────────

(rf/reg-event-fx
 :provisioning/apply-all
 (fn [{:keys [db]} _]
   {:db (-> db
            (assoc-in [:provisioning :plan-job :apply-cancelled?] false)
            (assoc-in [:provisioning :plan-job :applying?] true))
    :fx [[:dispatch [:provisioning/apply-next-chunk 0]]]}))

(rf/reg-event-fx
 :provisioning/apply-next-chunk
 (fn [{:keys [db]} [_ chunk-idx]]
   (let [plan-job (get-in db [:provisioning :plan-job])]
     (if (or (not plan-job) (:apply-cancelled? plan-job))
       {}
       (let [applicable (filterv #(contains? #{"Create" "Update"} (:status %)) (:items plan-job))
             chunk      (vec (take apply-chunk-size applicable))]
         (if (seq chunk)
           (let [chunk-keys (set (map :key chunk))
                 plan-ids   (mapv :plan-id chunk)]
             {:db (update-plan-items db #(set-status-where % (comp chunk-keys :key) "applying"))
              :fx [[:dispatch
                    [:fetch {:method     "POST"
                             :uri        "/resources/apply/batch"
                             :body       {:plan_ids plan-ids}
                             :on-success #(rf/dispatch [:provisioning/apply-batch-response chunk-idx chunk %])
                             :on-failure #(rf/dispatch [:provisioning/apply-batch-error chunk])}]]]})
           {:db (assoc-in db [:provisioning :plan-job :applying?] false)}))))))

(rf/reg-event-fx
 :provisioning/apply-batch-response
 (fn [{:keys [db]} [_ chunk-idx _chunk resp]]
   (let [result-map (into {} (map (fn [r] [(:plan_id r) r]) (:results resp)))
         apply-result (fn [it]
                        (if-let [r (get result-map (:plan-id it))]
                          (assoc it
                                 :status     (:status r)
                                 :session-id (:session_id r))
                          it))]
     {:db (update-plan-items db #(mapv apply-result %))
      :fx [[:dispatch [:provisioning/apply-next-chunk (inc chunk-idx)]]]})))

(rf/reg-event-fx
 :provisioning/apply-batch-error
 (fn [{:keys [db]} [_ chunk]]
   (let [chunk-keys (set (map :key chunk))]
     {:db (update-plan-items db #(set-status-where % (comp chunk-keys :key) "ApplyFailed"))
      :fx [[:dispatch [:provisioning/apply-next-chunk 0]]]})))

;; Single-item apply still uses the individual endpoint
(rf/reg-event-fx
 :provisioning/apply-plan
 (fn [{:keys [db]} [_ item-key]]
   (when-let [item (some #(when (= (:key %) item-key) %)
                         (get-in db plan-items-path))]
     {:db (update-plan-items db #(set-status-where % (key= item-key) "applying"))
      :fx [[:dispatch
            [:fetch {:method     "POST"
                     :uri        (str "/resources/" (:resource-name item) "/apply")
                     :body       {:plan_id (:plan-id item)}
                     :on-success #(rf/dispatch
                                   [:provisioning/apply-response item-key
                                    {:status     (:status %)
                                     :session-id (:session_id %)
                                     :plan-id    (:plan_id %)}])
                     :on-failure #(rf/dispatch
                                   [:provisioning/apply-response item-key
                                    {:status     "ApplyFailed"
                                     :session-id nil
                                     :plan-id    (:plan-id item)}])}]]]})))

(rf/reg-event-db
 :provisioning/apply-response
 (fn [db [_ item-key response]]
   (update-plan-items db
                      #(update-items-where % (key= item-key)
                                           (fn [it]
                                             (assoc it
                                                    :status     (:status response)
                                                    :session-id (:session-id response)))))))

;; ── Mock session output (dev fallback) ──────────────────────────────────────

(defn- mock-session-output
  "Generates realistic mock session output based on the item's current status."
  [item]
  (let [header (str "-- Session for plan: " (:plan-id item) "\n"
                    "-- Resource: " (:resource-name item) "\n"
                    "-- Role: " (:role item) "\n"
                    "-- Database: " (:database item) "\n"
                    "-- Permissions: " (:permissions item) "\n\n")
        schema (or (last (cs/split (or (:database item) "") #"\.")) "public")
        role   (:role item)
        rname  (:resource-name item)
        perms  (:permissions item)
        grant-sql
        (str "BEGIN;\n"
             "CREATE ROLE \"" role "\";\n"
             "GRANT CONNECT ON DATABASE \"" rname "\" TO \"" role "\";\n"
             "GRANT USAGE ON SCHEMA " schema " TO \"" role "\";\n"
             "GRANT " perms " ON ALL TABLES IN SCHEMA " schema
             " TO \"" role "\";\n"
             "ALTER DEFAULT PRIVILEGES IN SCHEMA " schema "\n"
             "  GRANT " perms " ON TABLES TO \"" role "\";\n"
             "COMMIT;\n\n")]
    (case (:status item)
      "Failed"
      (str header
           "ERROR: could not connect to server: Connection refused\n"
           "\tIs the server running on host \"" rname ".internal\"\n"
           "\tand accepting TCP/IP connections on port 5432?\n\n"
           "-- ✗ Plan failed after 30.0s (connection timeout)")

      "Create"
      (str header
           "-- DRY RUN: the following statements WILL be executed on apply\n\n"
           grant-sql
           "-- ✓ Plan: Create — role does not exist, will be created")

      "Update"
      (str header
           "-- DRY RUN: the following statements WILL be executed on apply\n\n"
           "BEGIN;\n"
           "-- Role \"" role "\" already exists, updating grants\n"
           "REVOKE ALL ON ALL TABLES IN SCHEMA " schema " FROM \"" role "\";\n"
           "GRANT USAGE ON SCHEMA " schema " TO \"" role "\";\n"
           "GRANT " perms " ON ALL TABLES IN SCHEMA " schema
           " TO \"" role "\";\n"
           "ALTER DEFAULT PRIVILEGES IN SCHEMA " schema "\n"
           "  GRANT " perms " ON TABLES TO \"" role "\";\n"
           "COMMIT;\n\n"
           "-- ✓ Plan: Update — role exists, grants will be refreshed")

      "Applied"
      (str header grant-sql "-- ✓ Applied successfully in 1.2s")

      "ApplyFailed"
      (str header
           "BEGIN;\n"
           "CREATE ROLE \"" role "\";\n"
           "ERROR: role \"" role "\" already exists\n"
           "ROLLBACK;\n\n"
           "-- ✗ Apply failed: duplicate role — resolve manually or retry with UPDATE strategy")

      (str header "-- Status: " (:status item)))))

(rf/reg-event-fx
 :provisioning/fetch-plan-session
 (fn [{:keys [db]} [_ session-id]]
   (let [item (some #(when (= (:session-id %) session-id) %)
                    (get-in db plan-items-path))]
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
