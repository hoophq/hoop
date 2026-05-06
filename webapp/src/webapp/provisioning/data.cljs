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

(defn- pg-schema-idents
  "Use the last segment of dotted names (e.g. dbprod.public → public) for mock SQL."
  [schemas]
  (mapv #(or (some-> (cs/split % #"\.") last) %) (vec schemas)))

;; ── CSV role import ──────────────────────────────────────────────────────────

(def ^:private valid-permissions
  #{"SELECT" "INSERT" "UPDATE" "DELETE" "ALL"})

(defn- parse-csv-line
  "Splits a CSV line on commas, respecting double-quoted fields."
  [line]
  (loop [chars (seq line) field [] fields [] in-quote? false]
    (if-not (seq chars)
      (conj fields (cs/trim (apply str field)))
      (let [c (first chars) rest-chars (rest chars)]
        (cond
          (and (= c \") (not in-quote?))
          (recur rest-chars field fields true)

          (and (= c \") in-quote?)
          (recur rest-chars field fields false)

          (and (= c \,) (not in-quote?))
          (recur rest-chars [] (conj fields (cs/trim (apply str field))) false)

          :else
          (recur rest-chars (conj field c) fields in-quote?))))))

(defn parse-csv-roles
  "Parses raw CSV text into a vector of maps.
   Expected columns: resource_name, role, database, permissions.
   Returns [{:resource-name :role :database :permissions :line-num} ...]."
  [csv-text]
  (let [lines  (cs/split-lines (cs/trim csv-text))
        header (first lines)
        data   (rest lines)]
    (when (and header (seq data))
      (vec
       (keep-indexed
        (fn [idx line]
          (let [trimmed (cs/trim line)]
            (when (seq trimmed)
              (let [fields (parse-csv-line trimmed)]
                {:resource-name (cs/trim (or (nth fields 0 nil) ""))
                 :role          (cs/trim (or (nth fields 1 nil) ""))
                 :database      (cs/trim (or (nth fields 2 nil) ""))
                 :permissions   (cs/trim (or (nth fields 3 nil) ""))
                 :line-num      (+ idx 2)}))))
        data)))))

(defn- normalize-permissions
  "Uppercases and re-joins permission tokens for consistent comparison."
  [perms-str]
  (->> (cs/split (cs/upper-case (cs/trim perms-str)) #"[,\s]+")
       (filter seq)
       sort
       (cs/join ", ")))

(defn- validate-permissions
  "Returns true if every token in the permissions string is recognized."
  [perms-str]
  (let [tokens (cs/split (cs/upper-case (cs/trim perms-str)) #"[,\s]+")]
    (and (seq tokens) (every? valid-permissions tokens))))

(defn classify-csv-rows
  "Takes parsed CSV rows and the selected resources.
   Returns {:valid [...] :conflicts {conflict-id [rows...]} :skipped-csv [...]
            :skipped-resources [...] :invalid [...]}."
  [parsed-rows resources]
  (let [resource-names (set (map :name resources))
        {matched true unmatched false}
        (group-by #(contains? resource-names (:resource-name %)) parsed-rows)

        {valid-fields true invalid-fields false}
        (group-by (fn [row]
                    (boolean
                     (and (seq (:resource-name row))
                          (seq (:role row))
                          (seq (:database row))
                          (seq (:permissions row)))))
                  (or matched []))

        {good-perms true bad-perms false}
        (group-by #(validate-permissions (:permissions %)) (or valid-fields []))

        dedup-groups (vals (group-by (fn [r] [(:resource-name r)
                                            (:role r)
                                            (:database r)
                                            (normalize-permissions (:permissions r))])
                                   (or good-perms [])))
        unique-rows    (mapv first dedup-groups)
        duplicate-rows (vec (mapcat rest dedup-groups))

        grouped (group-by (fn [r] [(:resource-name r) (:role r) (:database r)]) unique-rows)
        {conflict-groups true ok-groups false}
        (group-by (fn [[_k rows]] (> (count rows) 1)) grouped)

        conflict-map (into {}
                          (map-indexed
                           (fn [idx [k rows]]
                             [(str "conflict-" idx)
                              (mapv #(assoc % :conflict-key k) rows)])
                           (or conflict-groups [])))

        valid-rows (vec (mapcat val (or ok-groups [])))

        csv-resource-names     (set (map :resource-name (or parsed-rows [])))
        skipped-resources      (vec (filter #(not (contains? csv-resource-names (:name %)))
                                            resources))]
    {:valid             valid-rows
     :conflicts         conflict-map
     :duplicates        duplicate-rows
     :skipped-csv       (vec (or unmatched []))
     :skipped-resources skipped-resources
     :invalid           (vec (concat (or invalid-fields [])
                                     (map #(assoc % :error :bad-permissions) (or bad-perms []))))}))

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
              :agent-id \"default\"}"
  [{:keys [type targets configs roles-by-resource agent-id]}]
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
                                   (or (seq (get roles-by-resource (:id target)))
                                       [(str (:name target) "-readonly")
                                        (str (:name target) "-readwrite")]))
                 new-sessions
                 (mapv (fn [role-name]
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
                                          role-name agent-nm success?)})
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
