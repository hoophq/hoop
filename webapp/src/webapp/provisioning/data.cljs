(ns webapp.provisioning.data
  (:require [clojure.string :as cs]
            [re-frame.core :as rf]))

;; ── Resource stages ────────────────────────────────────────────────────────────
;; :needs-admin  → admin account not yet configured
;; :needs-roles  → admin configured, roles not yet provisioned
;; :ready        → fully provisioned

(def stage-label
  {:inventory "Inventory"
   :manage    "Manage"
   :provision "Provision"})

(def tab->stage
  {:manage    :needs-admin
   :provision :needs-roles})

;; ── Progress segments (3-step: discovered → admin → roles) ─────────────────────
(def segments
  [{:key :discovered :label "Discovered"}
   {:key :admin      :label "Admin account"}
   {:key :roles      :label "Roles configured"}])

(defn get-segment-state [seg-key resource]
  (let [stage (:stage resource)]
    (case seg-key
      :discovered "done"
      :admin      (if (= stage :needs-admin) "active" "done")
      :roles      (case stage
                    :ready       "done"
                    :needs-roles "active"
                    "locked")
      "locked")))

;; ── Progress segment visual lookup ─────────────────────────────────────────────
;; Single source of truth for both the bar color and the tooltip wording.
(def segment-states
  {"done"   {:bg "var(--green-9)"  :text "complete"}
   "active" {:bg "var(--indigo-9)" :text "action required"}
   "locked" {:bg "var(--gray-4)"   :text "complete previous steps first"}})

;; ── Stage → row action ─────────────────────────────────────────────────────────
;; Drives the per-row "Set up admin / Provision roles / Manage" buttons and
;; the stage-banner action button. `:handler-key` is looked up in the props
;; passed to `view`, so adding a new stage = one map entry.
(def stage-action
  {:needs-admin {:row-label   "Set up admin"
                 :banner-verb "Set up"
                 :handler-key :on-open-bulk-admin
                 :variant     "ghost"
                 :color       nil}
   :needs-roles {:row-label   "Provision roles"
                 :banner-verb "Provision"
                 :handler-key :on-open-bulk-roles
                 :variant     "ghost"
                 :color       nil}
   :ready       {:row-label   "Manage"
                 :banner-verb nil
                 :handler-key nil
                 :variant     "ghost"
                 :color       "gray"}})

;; ── DB type → Radix color ──────────────────────────────────────────────────────
(def db-type-color
  {"PostgreSQL" "blue"})

;; ── Mock agents ────────────────────────────────────────────────────────────────
(def mock-agents
  [{:id "default" :name "default-agent"      :env "All environments"}
   {:id "prod-us" :name "prod-agent-us-east" :env "Production"}
   {:id "staging" :name "staging-agent"      :env "Staging"}])

;; ── Mock PostgreSQL roles ──────────────────────────────────────────────────────
(def mock-pg-roles
  [{:name "pg_read_all_data"  :type "read"      :user-count 3}
   {:name "pg_write_all_data" :type "readwrite"  :user-count 1}
   {:name "pg_monitor"        :type "read"       :user-count 0}
   {:name "data_analyst"      :type "custom"     :user-count 5}
   {:name "app_readonly"      :type "read"       :user-count 8}
   {:name "app_readwrite"     :type "readwrite"  :user-count 2}])

(defn get-mock-roles [_db-type]
  mock-pg-roles)

;; ── Mock DB schemas (standard-role create preview) ─────────────────────────────
;; Each entry renders as one row in the create-mode table (per resource).
(def mock-resource-schemas
  "Illustrative schema names shown in the create-standard-roles UI."
  ["dbprod.public"
   "dbprod.private"
   "dbprod.enterprise"
   "dbprodw2.enterprise"
   "dbprodw2.enterprise"
   "dbprodw2.enterprise"
   "dbprode2.enterprise"
   "dbprod12.enterprise"
   "dbprod5.enterprise"
   "dbprod3.enterprise"])

;; Shown in bulk create-roles table and matched by mock session SQL for role-provision.
(def standard-role-readonly-permissions "SELECT")
(def standard-role-readwrite-permissions "SELECT, INSERT, UPDATE")

(defn standard-role-preview-rows
  "Two rows per resource × schema: one :readonly, one :readwrite.
   Each row carries `:role-type` (:readonly | :readwrite), `:role-name`,
   and `:permissions` so the UI can render a flat, checkable list."
  [resources]
  (vec
   (mapcat
    (fn [r]
      (mapcat
       (fn [[idx schema]]
         [{:key        (str (:id r) "-sch-" idx "-ro")
           :resource   r
           :schema     schema
           :role-type  :readonly
           :role-name  (str (:name r) "-readonly")
           :permissions standard-role-readonly-permissions}
          {:key        (str (:id r) "-sch-" idx "-rw")
           :resource   r
           :schema     schema
           :role-type  :readwrite
           :role-name  (str (:name r) "-readwrite")
           :permissions standard-role-readwrite-permissions}])
       (map-indexed vector mock-resource-schemas)))
    resources)))

(defn initial-create-selections
  "Default: every row selected."
  [resources]
  (into {} (map (fn [row] [(:key row) true])
                (standard-role-preview-rows resources))))

(defn create-schema-plan-from-selections
  "`selections` is {row-key bool}.
   Returns {resource-id {:readonly #{schema-str ...} :readwrite #{schema-str ...}}}."
  [resources selections]
  (reduce
   (fn [acc {:keys [key resource schema role-type]}]
     (let [selected? (get selections key true)]
       (if-not selected?
         acc
         (let [rid  (:id resource)
               kind (if (= role-type :readonly) :readonly :readwrite)
               cur  (get acc rid {:readonly #{} :readwrite #{}})]
           (assoc acc rid (update cur kind conj schema))))))
   {}
   (standard-role-preview-rows resources)))

(defn roles-from-create-schema-plan
  [target plan-by-resource]
  (let [p (get plan-by-resource (:id target))
        ro (seq (:readonly p))
        rw (seq (:readwrite p))]
    (cond-> []
      ro (conj (str (:name target) "-readonly"))
      rw (conj (str (:name target) "-readwrite")))))

(defn- pg-schema-idents
  "Use the last segment of dotted names (e.g. dbprod.public → public) for mock SQL."
  [schemas]
  (mapv #(or (some-> (cs/split % #"\.") last) %) (vec schemas)))

;; ── Helpers ────────────────────────────────────────────────────────────────────
(defn row-bg [stage selected? hovered?]
  (cond
    selected? "var(--indigo-2)"
    hovered?  "var(--indigo-1)"
    (= stage :needs-admin) "var(--amber-1)"
    (= stage :needs-roles) "var(--blue-1)"
    :else "var(--green-1)"))

(defn make-default-config []
  {:method "manual" :username "admin" :password ""})

(def role-type-color
  {"read"      "green"
   "readwrite" "blue"
   "admin"     "red"
   "custom"    "purple"})

;; ── Funnel accent colors / step labels ─────────────────────────────────────────
(def funnel-accent  ["var(--gray-8)" "var(--amber-9)" "var(--blue-9)"])
(def funnel-step-id ["01" "02" "03"])

;; ── Pluralize helper ───────────────────────────────────────────────────────────
(defn pluralize
  "Returns \"<n> <word>\" with the word pluralized when n != 1.
   Pass an explicit `plural` form for irregular plurals."
  ([n word] (pluralize n word (str word "s")))
  ([n word plural] (str n " " (if (= 1 n) word plural))))

;; ── Session output generator ───────────────────────────────────────────────────
(defn generate-session-output
  [job-type resource-name resource-type role-name agent-name success?
   & [{:keys [catalog-schemas]}]]
  (let [db-name        (cs/replace resource-name "-db" "")
        schema-idents  (pg-schema-idents (or (seq catalog-schemas) ["public"]))
        schema-banner  (when (seq catalog-schemas)
                         (str "-- Catalog schemas: " (cs/join ", " catalog-schemas) "\n"))]
    (if-not success?
      (str "-- Resource: " resource-name " (" resource-type ") | Agent: " agent-name "\n"
           "-- Target: " role-name "\n"
           (or schema-banner "")
           "\n"
           "ERROR: could not connect to server: Connection refused\n"
           "\tIs the server running on host \"" resource-name ".internal\"\n"
           "\tand accepting TCP/IP connections on port 5432?\n"
           "\n"
           "-- ✗ Failed after 30.0s (connection timeout)")
      (if (= job-type :admin-setup)
        (str "-- Resource: " resource-name " (" resource-type ") | Agent: " agent-name "\n"
             "-- Creating admin account: " role-name "\n"
             "\n"
             "BEGIN;\n"
             "CREATE USER \"" role-name "\" WITH ENCRYPTED PASSWORD '***' SUPERUSER;\n"
             "GRANT CONNECT ON DATABASE \"" db-name "\" TO \"" role-name "\";\n"
             "GRANT USAGE ON SCHEMA public TO \"" role-name "\";\n"
             "COMMIT;\n"
             "\n"
             "-- ✓ Admin account configured in 1.1s")
        (let [is-read? (and (cs/includes? role-name "read")
                            (not (cs/includes? role-name "write")))
              usage    (cs/join ""
                                  (for [sch schema-idents]
                                    (str "GRANT USAGE ON SCHEMA " sch " TO \"" role-name "\";\n")))
              tables   (cs/join ""
                                  (for [sch schema-idents]
                                    (str (if is-read?
                                           (str "GRANT SELECT ON ALL TABLES IN SCHEMA " sch " TO \"" role-name "\";\n")
                                           (str "GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA " sch
                                                " TO \"" role-name "\";\n")))))
              defaults (cs/join ""
                                  (for [sch schema-idents]
                                    (str "ALTER DEFAULT PRIVILEGES IN SCHEMA " sch "\n"
                                         (if is-read?
                                           (str "  GRANT SELECT ON TABLES TO \"" role-name "\";\n")
                                           (str "  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO \"" role-name "\";\n")))))]
          (str "-- Resource: " resource-name " (" resource-type ") | Agent: " agent-name "\n"
               "-- Role: " role-name "\n"
               (or schema-banner "")
               "\n"
               "BEGIN;\n"
               "CREATE ROLE \"" role-name "\";\n"
               "GRANT CONNECT ON DATABASE \"" db-name "\" TO \"" role-name "\";\n"
               usage
               tables
               defaults
               "COMMIT;\n"
               "\n"
               "-- ✓ Role provisioned in 0.9s"))))))

;; ── Job simulation ─────────────────────────────────────────────────────────────
(defn start-job!
  "Runs a mock provisioning job entirely client-side via js/setTimeout.
   Dispatches re-frame events to update application state.
   `opts` is {:type :admin-setup|:role-provision, :targets [...],
              :configs {id -> config}, :roles-by-resource {id -> [role-name ...]},
              :create-schema-plan {resource-id {:readonly #{schema...} :readwrite #{...}}} (optional),
              :agent-id \"default\"}"
  [{:keys [type targets configs roles-by-resource create-schema-plan agent-id]}]
  (let [job-id    (str "job-" (.now js/Date))
        agent-rec (or (some #(when (= (:id %) (or agent-id "default")) %) mock-agents)
                      (first mock-agents))
        agent-nm  (:name agent-rec)
        items     (mapv (fn [r] {:resource-id   (:id r)
                                 :resource-name (:name r)
                                 :resource-type (:db-type r)
                                 :status        "pending"})
                        targets)
        new-job   {:id         job-id
                   :type       type
                   :label      (str (if (= type :admin-setup) "Admin setup" "Role provisioning")
                                    " — " (count targets) " resources")
                   :items      items
                   :started-at (.now js/Date)}
        batch-size (max 1 (js/Math.ceil (/ (count targets) 8)))]

    (rf/dispatch [:provisioning/add-job new-job])

    (doseq [[i target] (map-indexed vector targets)]
      (let [batch   (js/Math.floor (/ i batch-size))
            run-at  (+ 400 (* batch 600))
            done-at (+ run-at 400)]

        ;; Mark running
        (js/setTimeout
         (fn []
           (rf/dispatch
            [:provisioning/update-jobs
             (fn [jobs]
               (mapv (fn [j]
                       (if (= (:id j) job-id)
                         (update j :items
                                 (fn [its]
                                   (mapv (fn [it]
                                           (if (= (:resource-id it) (:id target))
                                             (assoc it :status "running")
                                             it))
                                         its)))
                         j))
                     jobs))]))
         run-at)

        ;; Mark done/failed + create sessions
        (js/setTimeout
         (fn []
           (let [success?        (not= i (js/Math.floor (* (count targets) 0.85)))
                 roles           (if (= type :admin-setup)
                                   [(or (:username (get configs (:id target))) "admin")]
                                   (cond
                                     (seq (get roles-by-resource (:id target)))
                                     (get roles-by-resource (:id target))

                                     (seq create-schema-plan)
                                     (roles-from-create-schema-plan target create-schema-plan)

                                     :else
                                     [(str (:name target) "-readonly")
                                      (str (:name target) "-readwrite")]))
                 catalog-schemas (fn [role-name]
                                   (when (seq create-schema-plan)
                                     (let [p (get create-schema-plan (:id target)
                                                  {:readonly #{} :readwrite #{}})]
                                       (vec
                                        (sort
                                         (if (cs/includes? role-name "readwrite")
                                           (:readwrite p)
                                           (:readonly p)))))))
                 new-sessions
                 (mapv (fn [role-name]
                         (let [cats (catalog-schemas role-name)]
                           {:id            (str "sess-" job-id "-" (:id target) "-" role-name)
                            :job-id        job-id
                            :resource-id   (:id target)
                            :resource-name (:name target)
                            :resource-type (:db-type target)
                            :role-name     role-name
                            :status        (if success? "success" "error")
                            :started-at    (.now js/Date)
                            :duration-ms   (if success?
                                             (+ 700 (rand-int 800))
                                             30000)
                            :output        (generate-session-output
                                            type (:name target) (:db-type target)
                                            role-name agent-nm success?
                                            {:catalog-schemas (or cats [])})}))
                       roles)]

             (rf/dispatch [:provisioning/add-sessions new-sessions])
             (rf/dispatch
              [:provisioning/update-jobs
               (fn [jobs]
                 (mapv (fn [j]
                         (if (= (:id j) job-id)
                           (update j :items
                                   (fn [its]
                                     (mapv (fn [it]
                                             (if (= (:resource-id it) (:id target))
                                               (assoc it :status (if success? "done" "failed")
                                                      :session-ids (mapv :id new-sessions))
                                               it))
                                           its)))
                           j))
                       jobs))])))
         done-at)))

    ;; After all items finish, advance resource stages
    (let [finish-at (+ 400 (* (js/Math.ceil (/ (count targets) batch-size)) 600) 600)]
      (js/setTimeout
       (fn []
         (rf/dispatch
          [:provisioning/update-resources
           (fn [rs]
             (let [target-ids (set (map :id targets))]
               (mapv (fn [r]
                       (if (target-ids (:id r))
                         (if (= type :admin-setup)
                           (assoc r :stage :needs-roles
                                  :admin (or (:username (get configs (:id r))) "admin"))
                           (assoc r :stage :ready :role-count 2))
                         r))
                     rs)))]))
       finish-at))

    ;; Return job-id so callers can navigate to it
    job-id))
