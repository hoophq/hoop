(ns webapp.provisioning.data
  (:require [clojure.string :as cs]))

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
   :ready       {:row-label   "Configured"
                 :banner-verb nil
                 :handler-key nil
                 :variant     "ghost"
                 :color       "gray"}})


(def ^:private valid-permissions
  #{"SELECT" "INSERT" "UPDATE" "DELETE" "ALL"})

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

(defn- has-required-fields? [row]
  (and (seq (:resource-name row))
       (seq (:role row))
       (seq (:database row))
       (seq (:permissions row))))

(defn- dedup-group-key [r]
  [(:resource-name r) (:role r) (:database r)
   (normalize-permissions (:permissions r))])

(defn- conflict-group-key [r]
  [(:resource-name r) (:role r) (:database r)])

(defn- partition-by-pred
  "Like clojure.core/group-by with a boolean pred but returns
   {:matches [...] :rest [...]} for readability."
  [pred coll]
  (let [{t true f false} (group-by (comp boolean pred) coll)]
    {:matches (vec (or t [])) :rest (vec (or f []))}))

(defn- split-duplicates
  "Returns [unique-rows duplicate-rows] keeping the first occurrence of each
   (resource, role, database, permissions) tuple."
  [rows]
  (let [groups (vals (group-by dedup-group-key rows))]
    [(mapv first groups)
     (vec (mapcat rest groups))]))

(defn- split-conflicts
  "Returns [conflict-map valid-rows]. A conflict is the same
   (resource, role, database) appearing with different permissions."
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
        ;; ── 1. partition unmatched (resource not selected) from matched rows
        {matched :matches unmatched :rest}
        (partition-by-pred #(contains? resource-names (:resource-name %))
                           parsed-rows)

        ;; ── 2. drop rows missing required fields
        {valid-fields :matches missing-fields :rest}
        (partition-by-pred has-required-fields? matched)

        ;; ── 3. drop rows with malformed permissions
        {good-perms :matches bad-perms :rest}
        (partition-by-pred #(validate-permissions (:permissions %)) valid-fields)

        ;; ── 4. dedupe identical rows
        [unique-rows duplicate-rows] (split-duplicates good-perms)

        ;; ── 5. extract conflicts vs clean rows
        [conflict-map valid-rows]    (split-conflicts unique-rows)

        ;; ── 6. surface resources that have no CSV row at all
        csv-resource-names (set (map :resource-name (or parsed-rows [])))
        skipped-resources  (vec (remove #(contains? csv-resource-names (:name %))
                                        resources))]
    {:valid             valid-rows
     :conflicts         conflict-map
     :duplicates        duplicate-rows
     :skipped-csv       unmatched
     :skipped-resources skipped-resources
     :invalid           (into missing-fields
                              (map #(assoc % :error :bad-permissions) bad-perms))}))

;; ── Count helper ─────────────────────────────────────────────────────────────
(defn count-by-status
  "Count items whose :status is in the given set (or equals a single string)."
  [items statuses]
  (let [pred (if (set? statuses)
               #(contains? statuses (:status %))
               #(= statuses (:status %)))]
    (count (filter pred items))))

;; ── Plan-item status display config ─────────────────────────────────────────
;; Drives the badge/indicator in job-detail's status cell.
;; :spinner? true  → animated Loader2 instead of a badge
;; :icon     :check / :ban → leading icon inside the badge
(def plan-item-status
  {"pending"     {:color "gray"   :label "Pending"}
   "processing"  {:color "indigo" :label "Planning…" :spinner? true}
   "Create"      {:color "green"  :label "Create"}
   "Update"      {:color "blue"   :label "Update"}
   "Failed"      {:color "red"    :label "Failed"}
   "applying"    {:color "indigo" :label "Applying…" :spinner? true}
   "Applied"     {:color "green"  :label "Applied"   :icon :check}
   "ApplyFailed" {:color "red"    :label "Apply failed"}
   "Cancelled"   {:color "gray"   :label "Cancelled" :icon :ban}})

;; ── Plan-item action config ─────────────────────────────────────────────────
;; Maps status → action button shown on the right side of the status cell.
;; :event is the re-frame event keyword, :item-key selects what to pass from the item.
(def plan-item-action
  {"pending"     {:event :provisioning/cancel-plan-item :item-key :key
                  :variant "ghost" :color "gray" :icon :x :label nil}
   "Failed"      {:event :provisioning/retry-plan :item-key :key
                  :variant "soft" :color "red" :icon :refresh :label "Retry"}
   "Create"      {:event :provisioning/apply-plan :item-key :key
                  :variant "soft" :color "indigo" :icon :rocket :label "Apply"
                  :cancel? true}
   "Update"      {:event :provisioning/apply-plan :item-key :key
                  :variant "soft" :color "indigo" :icon :rocket :label "Apply"
                  :cancel? true}
   "ApplyFailed" {:event :provisioning/apply-plan :item-key :key
                  :variant "soft" :color "red" :icon :refresh :label "Retry"}})

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

;; ── Funnel accent colors / step labels ─────────────────────────────────────────
(def funnel-accent  ["var(--gray-8)" "var(--amber-9)" "var(--blue-9)"])
(def funnel-step-id ["01" "02" "03"])

;; ── Pluralize helper ───────────────────────────────────────────────────────────
(defn pluralize
  "Returns \"<n> <word>\" with the word pluralized when n != 1.
   Pass an explicit `plural` form for irregular plurals."
  ([n word] (pluralize n word (str word "s")))
  ([n word plural] (str n " " (if (= 1 n) word plural))))
