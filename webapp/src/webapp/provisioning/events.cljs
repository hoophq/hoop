(ns webapp.provisioning.events
  (:require [re-frame.core :as rf]))

(defn- update-items-where
  "Walk `items`, applying `f` to each item where `pred` returns truthy.
   Used by every event that mutates :provisioning/plan-job/:items."
  [items pred f]
  (mapv #(if (pred %) (f %) %) items))

(defn- set-status-where
  "Convenience wrapper: set :status on each item matching pred."
  [items pred new-status]
  (update-items-where items pred #(assoc % :status new-status)))

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

(defn- derive-stage [env]
  (if (get env :envvar:USER)
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
     :admin      (decode-env env "USER")
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

;; Resource update flow: fetch → merge envs → PUT

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
     :env-overrides (encode-envs {"USER" username
                                  "PASS" password})
     :agent-id      agent-id
     :on-success    on-success
     :on-failure    on-failure})
   {}))

;; Edits the inventory-level attributes (host, port) of an existing resource.
;; Mirrors `:provisioning/set-admin-credentials` but for the HOST/PORT envvars,
;; reusing `merge-and-put-resource!` so unrelated env vars (USER, PASS,
;; RESOURCE_CATALOG, …) are preserved across the PUT.
(rf/reg-event-fx
 :provisioning/set-inventory-attrs
 (fn [_ [_ {:keys [resource-name host port on-success on-failure]}]]
   (merge-and-put-resource!
    {:resource-name resource-name
     :env-overrides (encode-envs (cond-> {}
                                   (seq host) (assoc "HOST" host)
                                   (seq port) (assoc "PORT" (str port))))
     :on-success    on-success
     :on-failure    on-failure})
   {}))

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

(rf/reg-event-fx
 :provisioning/apply-inventory-next
 (fn [_ [_ {:keys [queue on-progress on-complete]}]]
   {:fx [[:dispatch
          [:provisioning/run-queue-next
           {:queue       queue
            :index       0
            :results     []
            :on-progress on-progress
            :on-complete on-complete
            :step-fn
            (fn [{:keys [resource-name host port]} continue!]
              (rf/dispatch
               [:provisioning/set-inventory-attrs
                {:resource-name resource-name
                 :host          host
                 :port          port
                 :on-success    (fn [name _resp] (continue! {:name name :status :success}))
                 :on-failure    (fn [name err]   (continue! {:name name :status :failed :error err}))}]))}]]]}))


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
   ;; Dedupe by :id (later writes win) so a synthesized skeleton can be
   ;; replaced by its loaded counterpart without piling up duplicate rows.
   ;; Preserves the original insertion order: existing entries stay in place
   ;; (with any matching new entry merged onto them), then truly new ids are
   ;; appended in the order they were passed in.
   (let [existing (or (get-in db [:provisioning :sessions]) [])
         by-id    (into {} (map (juxt :id identity)) new-sessions)
         seen-ids (set (map :id existing))
         merged   (mapv #(merge % (get by-id (:id %) {})) existing)
         appended (filterv #(not (contains? seen-ids (:id %))) new-sessions)]
     (assoc-in db [:provisioning :sessions] (into merged appended)))))


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
 :provisioning/clear-plan-job
 (fn [db _]
   ;; Drops both the current plan-job and any sessions tied to it. The
   ;; inventory banner is keyed off (seq (:items plan-job)), so removing
   ;; the job here also dismisses the banner. Sessions belonging to *other*
   ;; jobs (not this plan-job) are preserved.
   (let [job-id   (get-in db [:provisioning :plan-job :id])
         sessions (or (get-in db [:provisioning :sessions]) [])
         kept     (if job-id
                    (filterv #(not= job-id (:job-id %)) sessions)
                    sessions)]
     (-> db
         (assoc-in [:provisioning :plan-job] nil)
         (assoc-in [:provisioning :sessions] kept)))))

(rf/reg-event-db
 :provisioning/update-plan-item
 (fn [db [_ item-key update-fn]]
   (update-plan-items db #(update-items-where % (key= item-key) update-fn))))


(def ^:private plan-chunk-size 50)
(def ^:private apply-chunk-size 50)


(defn- plan-item-payload
  "Translates an in-memory plan-item into a ResourcePlanItem body for the API.
   The payload shape is type-aware: managed items send scopes + privileges;
   external items send source_role. Empty fields are still included for the
   non-active branch so the JSON encodes them as `[]` / `\"\"`, matching the
   backend's `binding:\"required\"` rules on each field (see openapi/types.go
   `ResourcePlanItem`) — the agent's planManaged / planExternal then
   reject the wrong combinations with a clear error."
  [item]
  (let [type (:type item)]
    (cond-> {:resource_name (:resource-name item)
             :type          type
             :role          (:role-input item)
             :scopes        (vec (:scopes item))
             :privileges    (vec (:privileges item))}
      (= type "external")
      (assoc :source_role (or (:source-role item) "")))))

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
                            ;; `:source-role` is carried through even on
                            ;; managed entries (where it'll be nil/empty)
                            ;; so the plan-item shape stays uniform; the
                            ;; payload encoder drops the field for non-
                            ;; external entries.
                            {:key           (str rid "-" idx)
                             :resource-id   rid
                             :resource-name (:name r)
                             :type          (:type role-entry)
                             :role-input    (:role role-entry)
                             :role-name     nil
                             :scopes        (vec (:scopes role-entry))
                             :privileges    (vec (:privileges role-entry))
                             :source-role   (:source-role role-entry)
                             :status        "pending"
                             :sid           nil})
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
                 payload    {:items (mapv plan-item-payload chunk)}]
             {:db (update-plan-items db #(set-status-where % (comp chunk-keys :key) "processing"))
              :fx [[:dispatch
                    [:fetch {:method     "POST"
                             :uri        "/resources/plan"
                             :body       payload
                             :on-success #(rf/dispatch [:provisioning/plan-batch-response chunk-idx chunk %])
                             :on-failure #(rf/dispatch [:provisioning/plan-batch-error chunk])}]]]})
           {:db (assoc-in db [:provisioning :plan-job :planning?] false)}))))))

(rf/reg-event-fx
 :provisioning/plan-batch-response
 (fn [{:keys [db]} [_ chunk-idx chunk resp]]
   ;; The plan API returns results in input order; match by index so we don't
   ;; rely on the (potentially server-generated) role name to correlate rows.
   (let [results       (vec (:results resp))
         results-by-key (into {}
                              (map-indexed (fn [idx it]
                                             [(:key it) (get results idx)])
                                           chunk))
         apply-result   (fn [it]
                          (if-let [r (get results-by-key (:key it))]
                            (assoc it
                                   :status    (:status r)
                                   :sid       (:sid r)
                                   :role-name (:role r)
                                   :message   (:message r))
                            it))]
     {:db (update-plan-items db #(mapv apply-result %))
      :fx [[:dispatch [:provisioning/plan-next-chunk (inc chunk-idx)]]]})))

(rf/reg-event-fx
 :provisioning/plan-batch-error
 (fn [{:keys [db]} [_ chunk]]
   (let [chunk-keys (set (map :key chunk))]
     {:db (update-plan-items db #(set-status-where % (comp chunk-keys :key) "failed"))
      :fx [[:dispatch [:provisioning/plan-next-chunk 0]]]})))

(rf/reg-event-db
 :provisioning/plan-response
 (fn [db [_ item-key response]]
   (update-plan-items db
                      #(update-items-where % (key= item-key)
                                           (fn [it]
                                             (assoc it
                                                    :status    (:status response)
                                                    :sid       (:sid response)
                                                    :role-name (:role-name response)
                                                    :message   (:message response)))))))

;; Single-item retry still uses the individual endpoint
(rf/reg-event-fx
 :provisioning/retry-plan
 (fn [{:keys [db]} [_ item-key]]
   (when-let [item (some #(when (= (:key %) item-key) %)
                         (get-in db plan-items-path))]
     (let [payload (plan-item-payload item)]
       {:db (update-plan-items db #(set-status-where % (key= item-key) "processing"))
        :fx [[:dispatch
              [:fetch {:method     "POST"
                       :uri        (str "/resources/" (:resource-name item) "/plan")
                       :body       payload
                       :on-success #(rf/dispatch
                                     [:provisioning/plan-response item-key
                                      {:status    (:status %)
                                       :sid       (:sid %)
                                       :role-name (:role %)
                                       :message   (:message %)}])
                       :on-failure #(rf/dispatch
                                     [:provisioning/plan-response item-key
                                      {:status "failed" :sid nil :role-name nil
                                       :message nil}])}]]]}))))

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
       (update-plan-items #(set-status-where % (comp #{"out-of-sync"} :status) "Cancelled")))))

(rf/reg-event-db
 :provisioning/cancel-plan-item
 (fn [db [_ item-key]]
   (let [cancellable? (fn [it]
                        (and (= (:key it) item-key)
                             (contains? #{"pending" "out-of-sync"} (:status it))))]
     (update-plan-items db #(set-status-where % cancellable? "Cancelled")))))

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
       (let [applicable (filterv #(contains? #{"out-of-sync"} (:status %)) (:items plan-job))
             chunk      (vec (take apply-chunk-size applicable))]
         (if (seq chunk)
           (let [chunk-keys (set (map :key chunk))
                 payload    {:items (mapv (fn [it]
                                            {:sid           (:sid it)
                                             :resource_name (:resource-name it)})
                                          chunk)}]
             {:db (update-plan-items db #(set-status-where % (comp chunk-keys :key) "applying"))
              :fx [[:dispatch
                    [:fetch {:method     "POST"
                             :uri        "/resources/apply"
                             :body       payload
                             :on-success #(rf/dispatch [:provisioning/apply-batch-response chunk-idx chunk %])
                             :on-failure #(rf/dispatch [:provisioning/apply-batch-error chunk])}]]]})
           {:db (assoc-in db [:provisioning :plan-job :applying?] false)}))))))

(rf/reg-event-fx
 :provisioning/apply-batch-response
 (fn [{:keys [db]} [_ chunk-idx chunk resp]]
   ;; The apply API preserves request order in its results array, so match by
   ;; index keyed on the row's :key. We can't correlate by :sid because the
   ;; gateway opens a fresh apply session and returns that new SID, not the
   ;; plan SID we sent in.
   (let [results        (vec (:results resp))
         results-by-key (into {}
                              (map-indexed (fn [idx it]
                                             [(:key it) (get results idx)])
                                           chunk))
         apply-result   (fn [it]
                          (if-let [r (get results-by-key (:key it))]
                            (assoc it
                                   :status  (:status r)
                                   ;; Point the row at the apply session so
                                   ;; "View session" opens the apply transcript
                                   ;; (CREATE ROLE / GRANT output) instead of
                                   ;; the plan dry-run.
                                   :sid     (or (:sid r) (:sid it))
                                   :message (:message r))
                            it))]
     {:db (update-plan-items db #(mapv apply-result %))
      :fx [[:dispatch [:provisioning/apply-next-chunk (inc chunk-idx)]]]})))

(rf/reg-event-fx
 :provisioning/apply-batch-error
 (fn [{:keys [db]} [_ chunk]]
   (let [chunk-keys (set (map :key chunk))]
     {:db (update-plan-items db #(set-status-where % (comp chunk-keys :key) "failed"))
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
                     :body       {:sid           (:sid item)
                                  :resource_name (:resource-name item)}
                     :on-success #(rf/dispatch
                                   [:provisioning/apply-response item-key
                                    {:status  (:status %)
                                     :sid     (:sid %)
                                     :message (:message %)}])
                     :on-failure #(rf/dispatch
                                   [:provisioning/apply-response item-key
                                    {:status  "failed"
                                     :sid     (:sid item)
                                     :message nil}])}]]]})))

(rf/reg-event-db
 :provisioning/apply-response
 (fn [db [_ item-key response]]
   (update-plan-items db
                      #(update-items-where % (key= item-key)
                                           (fn [it]
                                             (assoc it
                                                    :status  (:status response)
                                                    :sid     (or (:sid response) (:sid it))
                                                    :message (:message response)))))))

;; The gateway returns a session JSON with `event_stream` as an array. With
;; `?event_stream=utf8` that array becomes a single string with the concat-
;; enated stdout/stderr ("o" / "e" events). We use that format because it's
;; the easiest to display in the terminal-style cell.

(defn- extract-utf8-event-stream
  "Defensively pulls the displayable output from a gateway session response.
   Returns the empty string for any unexpected shape; never throws."
  [resp]
  (try
    (let [stream (when (map? resp) (:event_stream resp))]
      (cond
        (string? stream)                            stream
        (and (sequential? stream)
             (string? (first stream)))              (first stream)
        :else                                       ""))
    (catch :default _ "")))

(defn- compute-duration-ms
  "Best-effort duration from the session's start/end timestamps. Returns 0
   when either is missing or malformed."
  [resp]
  (try
    (let [start (:start_date resp)
          end   (:end_date resp)]
      (if (and start end)
        (max 0 (- (.getTime (js/Date. end))
                  (.getTime (js/Date. start))))
        0))
    (catch :default _ 0)))

(defn- item-status->session-status [item-status]
  (if (contains? #{"failed"} item-status)
    "error" "success"))

(rf/reg-event-fx
 :provisioning/load-job-sessions
 ;; Populates :provisioning :sessions with one skeleton entry per plan-item
 ;; that has a :sid. Skeletons carry status/role-name/resource info but no
 ;; output, and are flagged `:loaded? false` so the session-list view can
 ;; lazy-fetch the body when the user expands a row. Sessions that have
 ;; already been fetched (via the per-row "View session" button) keep their
 ;; loaded state thanks to `:provisioning/add-sessions`' dedupe-by-:id.
 (fn [{:keys [db]} _]
   (let [plan-job (get-in db [:provisioning :plan-job])
         items    (or (:items plan-job) [])
         job-id   (:id plan-job)
         existing (set (map :id (or (get-in db [:provisioning :sessions]) [])))
         now      (.now js/Date)
         skeletons (vec
                    (keep (fn [item]
                            (when (and (:sid item)
                                       (not (existing (:sid item))))
                              {:id            (:sid item)
                               :job-id        job-id
                               :resource-id   (:resource-id item)
                               :resource-name (:resource-name item)
                               :role-name     (or (:role-name item) (:role-input item))
                               :started-at    now
                               :status        (item-status->session-status (:status item))
                               :duration-ms   0
                               :output        ""
                               :loaded?       false}))
                          items))]
     (if (seq skeletons)
       {:fx [[:dispatch [:provisioning/add-sessions skeletons]]]}
       {}))))

(rf/reg-event-fx
 :provisioning/fetch-plan-session
 (fn [{:keys [db]} [_ sid]]
   (let [item (some #(when (= (:sid %) sid) %)
                    (get-in db plan-items-path))
         base {:id            sid
               :job-id        (get-in db [:provisioning :plan-job :id])
               :resource-id   (:resource-id item)
               :resource-name (:resource-name item)
               :role-name     (or (:role-name item) (:role-input item))
               :started-at    (.now js/Date)
               :status        (item-status->session-status (:status item))}]
     {:fx [[:dispatch
            [:fetch {:method     "GET"
                     :uri        (str "/sessions/" sid
                                      "?expand=event_stream&event_stream=utf8")
                     :on-success #(rf/dispatch [:provisioning/plan-session-fetched base %])
                     :on-failure #(rf/dispatch [:provisioning/plan-session-fetch-failed base %])}]]]})))

(rf/reg-event-fx
 :provisioning/plan-session-fetched
 (fn [_ [_ base resp]]
   (let [output      (extract-utf8-event-stream resp)
         duration-ms (compute-duration-ms (when (map? resp) resp))]
     {:fx [[:dispatch
            [:provisioning/add-sessions
             [(assoc base
                     :duration-ms duration-ms
                     :output      output
                     :loaded?     true)]]]]})))

(rf/reg-event-fx
 :provisioning/plan-session-fetch-failed
 (fn [_ [_ base err]]
   ;; Surface the failure inside the session row instead of dropping it —
   ;; the user already navigated to the session list and expects to see
   ;; *something*. The error session is appended like any other, so the
   ;; UI keeps rendering and the user can retry from the plan screen.
   (let [status   (or (:status err) "?")
         message  (or (:message err) "Unable to load session output.")
         body     (str "-- Failed to load session " (:id base) "\n"
                       "-- HTTP status: " status "\n"
                       "-- " message "\n\n"
                       "Tip: the session may not be available yet, or you may "
                       "not have permission to view it.")]
     {:fx [[:dispatch
            [:provisioning/add-sessions
             [(assoc base
                     :status      "error"
                     :duration-ms 0
                     :output      body
                     :loaded?     true)]]]]})))
