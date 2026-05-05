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

;; ── Session output generator ───────────────────────────────────────────────────
(defn generate-session-output
  [job-type resource-name resource-type role-name agent-name success?]
  (let [db-name (cs/replace resource-name "-db" "")]
    (if-not success?
      (str "-- Resource: " resource-name " (" resource-type ") | Agent: " agent-name "\n"
           "-- Target: " role-name "\n"
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
                            (not (cs/includes? role-name "write")))]
          (str "-- Resource: " resource-name " (" resource-type ") | Agent: " agent-name "\n"
               "-- Role: " role-name "\n"
               "\n"
               "BEGIN;\n"
               "CREATE ROLE \"" role-name "\";\n"
               "GRANT CONNECT ON DATABASE \"" db-name "\" TO \"" role-name "\";\n"
               "GRANT USAGE ON SCHEMA public TO \"" role-name "\";\n"
               (if is-read?
                 (str "GRANT SELECT ON ALL TABLES IN SCHEMA public TO \"" role-name "\";\n")
                 (str "GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO \"" role-name "\";\n"))
               "ALTER DEFAULT PRIVILEGES IN SCHEMA public\n"
               (if is-read?
                 (str "  GRANT SELECT ON TABLES TO \"" role-name "\";\n")
                 (str "  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO \"" role-name "\";\n"))
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
           (let [success?  (not= i (js/Math.floor (* (count targets) 0.85)))
                 roles     (if (= type :admin-setup)
                             [(or (:username (get configs (:id target))) "admin")]
                             (if (seq (get roles-by-resource (:id target)))
                               (get roles-by-resource (:id target))
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
                                  :admin-user (or (:username (get configs (:id r))) "admin"))
                           (assoc r :stage :ready :role-count 2))
                         r))
                     rs)))]))
       finish-at))

    ;; Return job-id so callers can navigate to it
    job-id))
