(ns webapp.provisioning.data
  (:require [clojure.string :as cs]))

;; Resource stages:
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

;; Single source of truth for both the bar color and the tooltip wording.
(def segment-states
  {"done"   {:bg "var(--green-9)"  :text "complete"}
   "active" {:bg "var(--indigo-9)" :text "action required"}
   "locked" {:bg "var(--gray-4)"   :text "complete previous steps first"}})

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
   :ready       {:row-label   "Configured"
                 :banner-verb nil
                 :handler-key nil
                 :variant     "ghost"
                 :color       "gray"}})


(def ^:private valid-permissions
  #{"SELECT" "INSERT" "UPDATE" "DELETE" "TRUNCATE" "REFERENCES" "TRIGGER" "CREATE" "EXECUTE"})

(def ^:private valid-types
  #{"managed" "external"})

(def ^:private valid-roles
  #{"ro" "rw"})

;; Separators for the two multi-value CSV columns. Scopes are strict (`;`)
;; because scope names are opaque and a stray `,` would silently split a
;; legitimate value. Permissions are a closed vocabulary (SELECT, INSERT, …),
;; so accepting `,`, `;`, or whitespace is unambiguous and matches how the
;; same list appears in actual SQL grant statements.
(def ^:private scope-sep      #";")
(def ^:private privileges-sep #"[,;\s]+")

(defn- tokenize
  "Split → trim → drop empties. Single owner of the tokenisation contract
   for multi-value CSV cells."
  [s sep]
  (->> (cs/split (or s "") sep)
       (map cs/trim)
       (filter seq)
       vec))

(defn split-csv-list
  "Tokenises a scopes cell. See `scope-sep` for the rationale on strictness."
  [s]
  (tokenize s scope-sep))

(defn split-privileges-list
  "Tokenises a permissions cell. See `privileges-sep` for the rationale on
   accepting `,`, `;`, or whitespace."
  [s]
  (tokenize s privileges-sep))

(defn- normalize-tokens
  "Case-folded, sorted, ', '-joined comparison fingerprint used by
   `dedup-group-key` / `conflict-group-key`. Goes through `tokenize` so the
   set of accepted separators stays in lock-step with the `split-*`
   functions."
  [s sep case-fn]
  (cs/join ", " (sort (tokenize (case-fn (or s "")) sep))))

(defn- normalize-scopes      [s] (normalize-tokens s scope-sep      cs/lower-case))
(defn- normalize-permissions [s] (normalize-tokens s privileges-sep cs/upper-case))

(defn- validate-permissions
  "Returns true if every token in the permissions string is recognized."
  [s]
  (let [tokens (mapv cs/upper-case (split-privileges-list s))]
    (and (seq tokens) (every? valid-permissions tokens))))

(defn- normalize-token
  "Canonical form for enum-ish single-value cells like `:type` and `:role`."
  [s]
  (cs/lower-case (cs/trim (or s ""))))

(defn- has-required-fields? [row]
  (and (seq (:resource-name row))
       (seq (:type row))
       (seq (:role row))
       (seq (:scopes row))
       (seq (:permissions row))))

(defn- valid-type-and-role? [row]
  (and (contains? valid-types (normalize-token (:type row)))
       (contains? valid-roles (normalize-token (:role row)))))

(defn- dedup-group-key [r]
  [(:resource-name r)
   (normalize-token (:type r))
   (normalize-token (:role r))
   (normalize-scopes (:scopes r))
   (normalize-permissions (:permissions r))])

(defn- conflict-group-key [r]
  [(:resource-name r)
   (normalize-token (:type r))
   (normalize-token (:role r))
   (normalize-scopes (:scopes r))])

(defn- partition-by-pred
  "Like clojure.core/group-by with a boolean pred but returns
   {:matches [...] :rest [...]} for readability."
  [pred coll]
  (let [{t true f false} (group-by (comp boolean pred) coll)]
    {:matches (vec (or t [])) :rest (vec (or f []))}))

(defn- split-duplicates
  "Returns [unique-rows duplicate-rows] keeping the first occurrence of each
   (resource, type, role, scopes, permissions) tuple."
  [rows]
  (let [groups (vals (group-by dedup-group-key rows))]
    [(mapv first groups)
     (vec (mapcat rest groups))]))

(defn- split-conflicts
  "Returns [conflict-map valid-rows]. A conflict is the same
   (resource, type, role, scopes) appearing with different permissions."
  [unique-rows]
  (let [grouped       (group-by conflict-group-key unique-rows)
        {conflict-groups true ok-groups false}
        (group-by (fn [[_k rows]] (> (count rows) 1)) grouped)
        conflict-map  (into {}
                            (map-indexed
                             (fn [idx [k rows]]
                               [(str "conflict-" idx)
                                (mapv #(assoc % :conflict-key k) rows)])
                             (or conflict-groups [])))
        valid-rows    (vec (mapcat val (or ok-groups [])))]
    [conflict-map valid-rows]))

(defn classify-csv-rows
  "Takes parsed CSV rows and the selected resources.
   Returns {:valid [...] :conflicts {conflict-id [rows...]}
            :duplicates [...] :skipped-csv [...] :skipped-resources [...]
            :invalid [...]}."
  [parsed-rows resources]
  (let [resource-names (set (map :name resources))
        ;; 1. partition unmatched (resource not selected) from matched rows
        {matched :matches unmatched :rest}
        (partition-by-pred #(contains? resource-names (:resource-name %))
                           parsed-rows)

        ;; 2. drop rows missing required fields
        {valid-fields :matches missing-fields :rest}
        (partition-by-pred has-required-fields? matched)

        ;; 3. drop rows with invalid type/role enum values
        {good-enums :matches bad-enums :rest}
        (partition-by-pred valid-type-and-role? valid-fields)

        ;; 4. drop rows with malformed permissions
        {good-perms :matches bad-perms :rest}
        (partition-by-pred #(validate-permissions (:permissions %)) good-enums)

        ;; 5. dedupe identical rows
        [unique-rows duplicate-rows] (split-duplicates good-perms)

        ;; 6. extract conflicts vs clean rows
        [conflict-map valid-rows]    (split-conflicts unique-rows)

        ;; 7. surface resources that have no CSV row at all
        csv-resource-names (set (map :resource-name (or parsed-rows [])))
        skipped-resources  (vec (remove #(contains? csv-resource-names (:name %))
                                        resources))]
    {:valid             valid-rows
     :conflicts         conflict-map
     :duplicates        duplicate-rows
     :skipped-csv       unmatched
     :skipped-resources skipped-resources
     :invalid           (into (mapv #(assoc % :error :missing-field) missing-fields)
                              (concat
                               (map #(assoc % :error :bad-role-type) bad-enums)
                               (map #(assoc % :error :bad-permissions) bad-perms)))}))

(defn count-by-status
  "Count items whose :status is in the given set (or equals a single string)."
  [items statuses]
  (let [pred (if (set? statuses)
               #(contains? statuses (:status %))
               #(= statuses (:status %)))]
    (count (filter pred items))))

;; Drives the badge/indicator in job-detail's status cell.
;; :spinner? true  → animated Loader2 instead of a badge
;; :icon     :check / :ban → leading icon inside the badge
;;
;; Status vocabulary tracks what the agent actually emits over the wire
;; (see `agent/controller/system/pgmanager/pgmanager.go`):
;;   plan response:  "in-sync" | "out-of-sync" | "failed"
;;   apply response: "success" | "failed"
;; The remaining keys (pending/processing/applying/Cancelled) are assigned
;; client-side as items move through the chunked plan/apply pipelines.
(def plan-item-status
  {"pending"      {:color "gray"   :label "Pending"}
   "processing"   {:color "indigo" :label "Planning…" :spinner? true}
   "in-sync"      {:color "green"  :label "In sync"    :icon :check}
   "out-of-sync"  {:color "green"  :label "Plan ready"}
   "failed"       {:color "red"    :label "Failed"}
   "applying"     {:color "indigo" :label "Applying…" :spinner? true}
   "success"      {:color "green"  :label "Applied"   :icon :check}
   "Cancelled"    {:color "gray"   :label "Cancelled" :icon :ban}})

;; Maps status → action button shown on the right side of the status cell.
;; :event is the re-frame event keyword, :item-key selects what to pass from the item.
;; "in-sync" items get no action — the role already matches the desired state.
(def plan-item-action
  {"pending"     {:event :provisioning/cancel-plan-item :item-key :key
                  :variant "ghost" :color "gray" :icon :x :label nil}
   "failed"      {:event :provisioning/retry-plan :item-key :key
                  :variant "soft" :color "red" :icon :refresh :label "Retry"}
   "out-of-sync" {:event :provisioning/apply-plan :item-key :key
                  :variant "soft" :color "indigo" :icon :rocket :label "Apply"
                  :cancel? true}})

(defn row-bg [stage selected? hovered?]
  (cond
    selected? "var(--indigo-2)"
    hovered?  "var(--indigo-1)"
    (= stage :needs-admin) "var(--amber-1)"
    (= stage :needs-roles) "var(--blue-1)"
    :else "var(--green-1)"))

(defn make-default-config []
  {:method "manual" :username "admin" :password ""})

(def funnel-accent  ["var(--gray-8)" "var(--amber-9)" "var(--blue-9)"])
(def funnel-step-id ["01" "02" "03"])

(defn pluralize
  "Returns \"<n> <word>\" with the word pluralized when n != 1.
   Pass an explicit `plural` form for irregular plurals."
  ([n word] (pluralize n word (str word "s")))
  ([n word plural] (str n " " (if (= 1 n) word plural))))
