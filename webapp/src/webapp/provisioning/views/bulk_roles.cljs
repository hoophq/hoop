(ns webapp.provisioning.views.bulk-roles
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Card Checkbox
                               Flex Skeleton Text]]
   ["lucide-react" :refer [AlertTriangle Check ChevronDown
                           ChevronRight Circle FileText Info Key
                           Upload XCircle]]
   ["react" :as react]
   [webapp.provisioning.data :as data]
   [webapp.provisioning.views.shared :as shared]))

;; ── Method card ────────────────────────────────────────────────────────────────
(defn method-card [{:keys [selected icon title description badge on-click]}]
  [:> Card {:style {:flex 1 :cursor "pointer" :position "relative"
                    :border-color (when selected "var(--indigo-9)")
                    :border-width (if selected 2 1)
                    :background   (when selected "var(--indigo-2)")}
            :on-click on-click}
   (when selected
     [:> Box {:style {:position "absolute" :top 10 :right 10
                      :width 18 :height 18 :border-radius "50%"
                      :background "var(--indigo-9)"
                      :display "flex" :align-items "center" :justify-content "center"
                      :color "white"}}
      [:> Check {:size 10}]])
   [:> Flex {:direction "column" :gap "2" :p "1"}
    [:> Box {:style {:width 32 :height 32 :border-radius "var(--radius-2)"
                     :display "flex" :align-items "center" :justify-content "center"
                     :background (if selected "var(--indigo-4)" "var(--gray-3)")
                     :color      (if selected "var(--indigo-11)" "var(--gray-9)")
                     :margin-bottom 2}}
     icon]
    [:> Flex {:direction "column" :gap "1"}
     [:> Flex {:align "center" :gap "1"}
      [:> Text {:size "2" :weight "medium"} title]
      (when badge [:> Badge {:color "indigo" :variant "soft" :size "1"} badge])]
     [:> Text {:size "1" :color "gray"} description]]]])

;; ── Role discovery table (bind mode) ──────────────────────────────────────────
(defn role-discovery-table [{:keys [resources discovered-roles selected-roles on-toggle]}]
  (let [all-roles (mapcat (fn [r]
                            (map (fn [role] {:resource r :role role})
                                 (get discovered-roles (:id r) [])))
                          resources)]
    [:> Box {:style {:flex 1 :overflow-y "auto"
                     :border "1px solid var(--gray-5)"
                     :border-radius "var(--radius-2)"}}
     [shared/flex-table-header
      [{:width 36  :label ""}
       {:flex "2 1 0" :label "Resource"}
       {:flex "2 1 0" :label "Role"}
       {:width 100 :label "Type"}
       {:width 80  :label "DB users"}]]
     (doall
      (for [[i {:keys [resource role]}] (map-indexed vector all-roles)]
        (let [selected? (contains? (get selected-roles (:id resource)) (:name role))]
          ^{:key (str (:id resource) "-" (:name role))}
          [:> Flex {:px "3" :py "2" :align "center"
                    :on-click #(on-toggle (:id resource) (:name role))
                    :style {:border-bottom (when (< i (dec (count all-roles)))
                                             "1px solid var(--gray-3)")
                            :min-height 44
                            :background (if selected?
                                          "var(--indigo-1)"
                                          (shared/zebra-bg i))
                            :cursor "pointer"}}
           [:> Box {:style {:width 36 :flex-shrink 0}
                    :on-click #(.stopPropagation %)}
            [:> Checkbox {:checked selected?
                          :onCheckedChange #(on-toggle (:id resource) (:name role))}]]
           [:> Flex {:align "center" :gap "2" :style {:flex "2 1 0"}}
            [:> Text {:size "2" :weight "medium"} (:name resource)]
            [:> Badge {:color "gray" :variant "soft" :size "1"} (:db-type resource)]]
           [:> Box {:style {:flex "2 1 0"}}
            [:> Text {:size "2" :style {:font-family "var(--font-mono)" :font-size 12}}
             (:name role)]]
           [:> Box {:style {:width 100 :flex-shrink 0}}
            [:> Badge {:color (get data/role-type-color (:type role) "gray")
                       :variant "soft" :size "1"}
             (:type role)]]
           [:> Box {:style {:width 80 :flex-shrink 0}}
            [:> Text {:size "2" :color "gray"} (:user-count role)]]])))]))

;; ── CSV preview table header ─────────────────────────────────────────────────
(def ^:private csv-preview-cols
  [{:flex "1.2 1 0" :label "Resource"}
   {:flex "1.2 1 0" :label "Role"}
   {:flex "1 1 0"   :label "Database"}
   {:flex "1 1 0"   :label "Permissions"}
   {:width 110      :label "Status"}])

;; ── Row renderers ────────────────────────────────────────────────────────────
(defn- csv-row-base [{:keys [row bg extra-style badge-content badge-color badge-icon reason]}]
  (let [dimmed? (contains? #{"gray"} badge-color)]
    [:> Flex {:direction "column"
              :style (merge {:background bg} extra-style)}
     [:> Flex {:px "3" :py "2" :align "center" :style {:min-height 40}}
      [:> Flex {:align "center" :gap "2" :style {:flex "1.2 1 0" :min-width 0}}
       [:> Text {:size "2" :style (when dimmed? {:color "var(--gray-8)"})}
        (:resource-name row)]]
      [:> Box {:style {:flex "1.2 1 0" :min-width 0}}
       [:> Text {:size "2" :style {:font-family "var(--font-mono)" :font-size 12
                                   :color (when dimmed? "var(--gray-8)")}}
        (:role row)]]
      [:> Box {:style {:flex "1 1 0" :min-width 0}}
       [:> Text {:size "2" :style {:font-family "var(--font-mono)" :font-size 12
                                   :color (when dimmed? "var(--gray-8)")}}
        (:database row)]]
      [:> Box {:style {:flex "1 1 0" :min-width 0}}
       [:> Text {:size "1" :color "gray"} (:permissions row)]]
      [:> Box {:style {:width 110 :flex-shrink 0}}
       [:> Badge {:color badge-color :variant "soft" :size "1"}
        badge-icon " " badge-content]]]
     (when reason
       [:> Flex {:px "3" :pb "2" :style {:margin-top -4}}
        [:> Text {:size "1" :style {:color "var(--gray-9)" :font-style "italic"}}
         reason]])]))

(defn- valid-row [row idx total]
  [csv-row-base {:row row
                 :bg (shared/zebra-bg idx)
                 :extra-style {:border-bottom (when (< idx (dec total)) "1px solid var(--gray-3)")}
                 :badge-content "Valid"
                 :badge-color "green"
                 :badge-icon [:> Check {:size 10}]}])

(defn- skipped-csv-row [row idx total]
  [csv-row-base {:row row
                 :bg "var(--gray-2)"
                 :extra-style {:border-bottom (when (< idx (dec total)) "1px solid var(--gray-3)")
                               :opacity 0.65}
                 :badge-content "Skipped"
                 :badge-color "gray"
                 :badge-icon [:> Circle {:size 10}]
                 :reason "Resource not in selection"}])

(defn- duplicate-row [row idx total]
  [csv-row-base {:row row
                 :bg "var(--gray-2)"
                 :extra-style {:border-bottom (when (< idx (dec total)) "1px solid var(--gray-3)")
                               :opacity 0.65}
                 :badge-content "Duplicate"
                 :badge-color "gray"
                 :badge-icon [:> Circle {:size 10}]
                 :reason (str "Duplicate of line with same resource, role, database & permissions")}])

(defn- invalid-row [row idx total]
  (let [bad-perms? (= (:error row) :bad-permissions)
        reason     (if bad-perms? "Bad permissions" "Missing field")]
    [csv-row-base {:row row
                   :bg "var(--red-2)"
                   :extra-style {:border-bottom (when (< idx (dec total)) "1px solid var(--gray-3)")}
                   :badge-content reason
                   :badge-color "red"
                   :badge-icon [:> XCircle {:size 10}]
                   :reason (if bad-perms?
                             "Permissions must be valid SQL grants (SELECT, INSERT, UPDATE, DELETE, ALL)"
                             "All columns are required: resource_name, role, database, permissions")}]))

;; ── Conflict group ───────────────────────────────────────────────────────────
(defn- conflict-group-rows [conflict-id rows conflict-picks set-conflict-picks]
  (let [picked (get conflict-picks conflict-id)]
    [:> Box {:style {:border-left "3px solid var(--amber-8)" :margin-bottom 2}}
     (doall
      (for [[i row] (map-indexed vector rows)]
        (let [line   (:line-num row)
              active? (= picked line)]
          ^{:key (str conflict-id "-" line)}
          [:> Flex {:px "3" :py "2" :align "center"
                    :on-click #(set-conflict-picks (assoc conflict-picks conflict-id line))
                    :style {:min-height 40 :cursor "pointer"
                            :background (if active? "var(--amber-2)" "var(--gray-2)")
                            :opacity (if (and picked (not active?)) 0.45 1)
                            :border-bottom (when (< i (dec (count rows)))
                                             "1px solid var(--amber-4)")}}
           [:> Box {:style {:width 24 :flex-shrink 0 :margin-right 8}}
            [:> Box {:style {:width 16 :height 16 :border-radius "50%"
                             :border (str "2px solid " (if active? "var(--amber-9)" "var(--gray-7)"))
                             :background (when active? "var(--amber-9)")
                             :display "flex" :align-items "center" :justify-content "center"}}
             (when active?
               [:> Box {:style {:width 6 :height 6 :border-radius "50%"
                                :background "white"}}])]]
           [:> Flex {:align "center" :gap "2" :style {:flex "1.2 1 0" :min-width 0}}
            [:> Text {:size "2"} (:resource-name row)]
            [:> Badge {:color "amber" :variant "soft" :size "1"} (str "line " (:line-num row))]]
           [:> Box {:style {:flex "1.2 1 0" :min-width 0}}
            [:> Text {:size "2" :style {:font-family "var(--font-mono)" :font-size 12}}
             (:role row)]]
           [:> Box {:style {:flex "1 1 0" :min-width 0}}
            [:> Text {:size "2" :style {:font-family "var(--font-mono)" :font-size 12}}
             (:database row)]]
           [:> Box {:style {:flex "1 1 0" :min-width 0}}
            [:> Text {:size "1" :color "gray"} (:permissions row)]]
           [:> Box {:style {:width 110 :flex-shrink 0}}
            [:> Badge {:color "amber" :variant "soft" :size "1"}
             [:> AlertTriangle {:size 10}] " Conflict"]]])))]))

;; ── Skipped resources section ────────────────────────────────────────────────
(defn- skipped-resources-section [skipped-resources]
  (when (seq skipped-resources)
    [:> Box {:mt "3" :p "3"
             :style {:border "1px solid var(--gray-5)"
                     :border-radius "var(--radius-2)"
                     :background "var(--gray-2)"}}
     [:> Flex {:align "center" :gap "2" :mb "2"}
      [:> Info {:size 14 :color "var(--gray-9)"}]
      [:> Text {:size "2" :weight "medium" :color "gray"}
       (str (data/pluralize (count skipped-resources) "selected resource")
            " had no matching CSV rows")]]
     [:> Flex {:gap "2" :style {:flex-wrap "wrap"}}
      (for [r skipped-resources]
        ^{:key (:id r)}
        [:> Badge {:color "gray" :variant "soft" :size "1"} (:name r)])]]))

;; ── CSV parsed preview ───────────────────────────────────────────────────────
(defn- csv-preview [{:keys [classification conflict-picks set-conflict-picks]}]
  (let [{:keys [valid conflicts duplicates skipped-csv skipped-resources invalid]}
        (or classification {})
        has-conflicts?  (seq conflicts)
        has-invalid?    (seq invalid)

        flat-rows (vec (concat
                        (mapv (fn [row] {:type :valid :row row}) valid)
                        (mapv (fn [row] {:type :duplicate :row row}) duplicates)
                        (mapv (fn [row] {:type :invalid :row row}) invalid)
                        (mapv (fn [row] {:type :skipped :row row}) skipped-csv)))
        total     (count flat-rows)]
    [:<>
     (when (or has-conflicts? has-invalid?)
       [shared/info-callout
        {:color (if has-conflicts? "amber" "red")
         :mb    "3"
         :size  "1"
         :icon  (if has-conflicts?
                  [:> AlertTriangle {:size 14}]
                  [:> XCircle {:size 14}])
         :text  (cond
                  (and has-conflicts? has-invalid?)
                  "Some rows have conflicts or errors. Resolve all conflicts (pick one row per group) and fix invalid rows before applying."
                  has-conflicts?
                  "Some rows have the same resource, role, and database but different permissions. Pick which row to keep for each conflict group."
                  :else
                  "Some rows have missing fields or invalid permissions. They will be excluded from provisioning.")}])

     [:> Box {:style {:flex 1 :overflow-y "auto"
                      :border "1px solid var(--gray-5)"
                      :border-radius "var(--radius-2)"}}
      [shared/flex-table-header csv-preview-cols]

      (into [:<>]
            (when has-conflicts?
              (for [[cid rows] (sort-by key conflicts)]
                ^{:key cid}
                [conflict-group-rows cid rows conflict-picks set-conflict-picks])))

      (into [:<>]
            (for [[i {:keys [type row]}] (map-indexed vector flat-rows)]
              (let [row-key (str (name type) "-" (:line-num row))]
                (case type
                  :valid     ^{:key row-key} [valid-row row i total]
                  :duplicate ^{:key row-key} [duplicate-row row i total]
                  :invalid   ^{:key row-key} [invalid-row row i total]
                  :skipped   ^{:key row-key} [skipped-csv-row row i total]))))]

     [skipped-resources-section skipped-resources]]))

;; ── CSV row normalization ────────────────────────────────────────────────────
(defn- normalize-roles-rows
  "Converts raw papa-parsed rows into the role-row shape the classifier expects."
  [rows]
  (vec
   (map-indexed
    (fn [idx row]
      {:resource-name (or (:resource_name row) "")
       :role          (or (:role row) "")
       :database      (or (:database row) "")
       :permissions   (or (:permissions row) "")
       :line-num      (+ idx 2)})
    rows)))

;; ── Main screen (inner, uses React hooks) ────────────────────────────────────
(defn- bulk-roles-screen-inner
  [{:keys [resources on-apply on-cancel initial-method]}]
  (let [[method set-method]                     (react/useState (or initial-method "csv"))
        [csv-parsing set-csv-parsing]           (react/useState false)
        [csv-parsed set-csv-parsed]             (react/useState nil)
        [classification set-classification]     (react/useState nil)
        [conflict-picks set-conflict-picks]     (react/useState {})
        [discovered-roles set-discovered-roles] (react/useState {})
        [roles-loading set-roles-loading]       (react/useState false)
        [selected-roles set-selected-roles]     (react/useState {})
        [res-list-open set-res-list-open]       (react/useState false)
        load-timer-ref                          (react/useRef nil)

        handle-file! (fn [file]
                       (set-csv-parsing true)
                       (shared/parse-csv!
                        file
                        {:on-complete
                         (fn [rows]
                           (let [parsed     (normalize-roles-rows rows)
                                 classified (data/classify-csv-rows parsed resources)]
                             (set-csv-parsed parsed)
                             (set-classification classified)
                             (set-conflict-picks {})
                             (set-csv-parsing false)))}))

        clear-csv!   (fn []
                       (set-csv-parsed nil)
                       (set-classification nil)
                       (set-conflict-picks {}))

        loading?     roles-loading

        total-discovered (reduce + 0 (map count (vals discovered-roles)))
        total-selected   (reduce + 0 (map count (vals selected-roles)))

        toggle-role  (fn [resource-id role-name]
                       (set-selected-roles
                        (fn [sr]
                          (let [s (get sr resource-id #{})]
                            (assoc sr resource-id
                                   (if (contains? s role-name)
                                     (disj s role-name)
                                     (conj s role-name)))))))

        cur-picks      conflict-picks
        unresolved     (when classification
                         (count (filter (fn [[cid _]]
                                         (nil? (get cur-picks cid)))
                                       (:conflicts classification))))

        apply-disabled? (or (and (= method "csv")
                                 (or (nil? classification)
                                     (and (seq (:conflicts classification))
                                          (pos? (or unresolved 0)))))
                            (and (= method "bind") (or loading? (zero? total-selected))))

        valid-count    (count (:valid classification))
        conflict-count (count (:conflicts classification))
        duplicate-count (count (:duplicates classification))
        skipped-count  (+ (count (:skipped-csv classification))
                          (count (:skipped-resources classification)))

        footer-info (cond
                      (and (= method "csv") classification)
                      (str valid-count " valid"
                           (when (pos? duplicate-count)
                             (str " · " (data/pluralize duplicate-count "duplicate")))
                           " · " (data/pluralize conflict-count "conflict")
                           " · " skipped-count " skipped")
                      (and (= method "bind") loading?)
                      "Reading roles from databases…"
                      (= method "bind")
                      (str total-discovered " roles discovered · " total-selected " selected")
                      :else "Upload a CSV to continue")]

    [:> Flex {:direction "column" :style {:flex 1 :min-height 0}}
     [shared/bulk-screen-header {:title          "Provision — roles"
                                 :resource-count (count resources)
                                 :on-back        on-cancel}]

     ;; Method cards
     [:> Flex {:gap "3" :mb "5"}
      [method-card {:selected (= method "csv")
                    :icon     [:> Upload {:size 18}]
                    :title    "Import from CSV"
                    :description "Define roles in a CSV file and bulk-apply across all resources."
                    :badge    "Recommended"
                    :on-click (fn []
                                (set-method "csv")
                                (when (.-current load-timer-ref)
                                  (js/clearTimeout (.-current load-timer-ref))))}]
      [method-card {:selected (= method "bind")
                    :icon     [:> Key {:size 18}]
                    :title    "Bind existing roles"
                    :description "Read and select roles from the database — no new roles created."
                    :on-click (fn []
                                (set-method "bind")
                                (set-roles-loading true)
                                (set-selected-roles {})
                                (when (.-current load-timer-ref)
                                  (js/clearTimeout (.-current load-timer-ref)))
                                (set! (.-current load-timer-ref)
                                      (js/setTimeout
                                       (fn []
                                         (set-discovered-roles
                                          (into {} (map (fn [r]
                                                          [(:id r) (data/get-mock-roles (:db-type r))])
                                                        resources)))
                                         (set-roles-loading false))
                                       1500)))}]]

     ;; ── CSV mode — upload ──
     (when (and (= method "csv") (nil? csv-parsed))
       [:> Flex {:direction "column" :gap "3" :style {:flex 1}}
        ;; Collapsible selected-resources list
        [:> Box {:style {:border "1px solid var(--gray-5)"
                         :border-radius "var(--radius-2)"
                         :background "var(--gray-2)"}}
         [:> Flex {:align "center" :gap "2" :px "3" :py "2"
                   :on-click #(set-res-list-open (not res-list-open))
                   :style {:cursor "pointer" :user-select "none"}}
          (if res-list-open
            [:> ChevronDown {:size 14 :color "var(--gray-9)"}]
            [:> ChevronRight {:size 14 :color "var(--gray-9)"}])
          [:> Text {:size "2" :weight "medium" :color "gray"}
           (data/pluralize (count resources) "selected resource")]
          [:> Text {:size "1" :color "gray" :style {:margin-left "auto"}}
           "Use these names in the resource_name column"]]
         (when res-list-open
           [:> Flex {:gap "2" :px "3" :pb "3" :style {:flex-wrap "wrap"}}
            (for [r (sort-by :name resources)]
              ^{:key (:id r)}
              [:> Badge {:color "gray" :variant "outline" :size "1"
                         :style {:font-family "var(--font-mono)" :font-size 11}}
               (:name r)])])]

        [shared/csv-drop-zone {:on-file   handle-file!
                               :hint-text "Columns: resource_name, role, database, permissions"
                               :loading?  csv-parsing}]
        [:> Flex {:justify "end"}
         [:> Button {:variant "ghost" :size "1" :color "gray"}
          [:> FileText {:size 12}] " Download template"]]])

     ;; ── CSV mode — parsed preview ──
     (when (and (= method "csv") csv-parsed)
       [:<>
        [:> Flex {:align "center" :justify "between" :mb "3"}
         [:> Text {:size "2" :weight "medium"}
          (str (count csv-parsed) " rows parsed")]
         [:> Button {:variant "ghost" :size "1" :color "gray"
                     :on-click clear-csv!}
          "Upload different file"]]
        [csv-preview {:classification     classification
                      :conflict-picks     cur-picks
                      :set-conflict-picks set-conflict-picks}]])

     ;; ── Bind mode — loading ──
     (when (and (= method "bind") loading?)
       [:> Box {:style {:flex 1 :border "1px solid var(--gray-5)"
                        :border-radius "var(--radius-2)" :overflow "hidden"}}
        [:> Flex {:px "3" :py "2"
                  :style {:background "var(--gray-3)"
                          :border-bottom "1px solid var(--gray-5)"}}
         [:> Text {:size "1" :color "gray" :weight "medium"}
          (str "Reading roles from " (count resources) " resources…")]]
        (for [i (range 8)]
          ^{:key i}
          [:> Flex {:px "3" :py "3" :align "center" :gap "3"
                    :style {:border-bottom (when (< i 7) "1px solid var(--gray-3)")}}
           [:> Skeleton {:width "20px" :height "16px"}]
           [:> Skeleton {:width "140px" :height "14px"}]
           [:> Skeleton {:width "120px" :height "14px"}]
           [:> Skeleton {:width "70px" :height "20px"}]
           [:> Skeleton {:width "50px" :height "14px"}]])])

     ;; ── Bind mode — role selection ──
     (when (and (= method "bind") (not loading?))
       [:<>
        [shared/info-callout
         {:color "blue" :mb "3" :size "1"
          :icon  [:> Info {:size 14}]
          :text  "Select the roles to bring into Hoop. A Hoop user will be created for each selected role and bound to the existing database role — no new roles are created in the database."}]
        [role-discovery-table
         {:resources        resources
          :discovered-roles discovered-roles
          :selected-roles   selected-roles
          :on-toggle        toggle-role}]])

     ;; Footer
     [shared/bulk-footer
      {:info-text       footer-info
       :on-cancel       on-cancel
       :apply-disabled? apply-disabled?
       :apply-label     (cond
                          (= method "bind")
                          (str "Bind " (data/pluralize total-selected "role") " →")
                          (= method "csv")
                          (str "Provision " (+ valid-count
                                                (count (filter #(get cur-picks (key %))
                                                               (:conflicts (or classification {})))))
                               " roles →")
                          :else
                          (str "Provision " (data/pluralize (count resources) "resource") " →"))
       :on-apply        (fn []
                          (let [roles-by-resource
                                (cond
                                  (= method "csv")
                                  (when classification
                                    (let [kept-conflict-rows
                                          (vec (keep (fn [[cid rows]]
                                                       (let [ln (get cur-picks cid)]
                                                         (some #(when (= (:line-num %) ln) %) rows)))
                                                     (:conflicts classification)))
                                          all-valid (concat (:valid classification) kept-conflict-rows)
                                          by-name   (group-by :resource-name all-valid)
                                          res-by-name (into {} (map (fn [r] [(:name r) r]) resources))]
                                      (into {}
                                            (keep (fn [[rname rows]]
                                                    (when-let [r (get res-by-name rname)]
                                                      [(:id r)
                                                       (vec (map (fn [row]
                                                                   {:role        (:role row)
                                                                    :database    (:database row)
                                                                    :permissions (:permissions row)})
                                                                 rows))]))
                                                  by-name))))

                                  (= method "bind")
                                  (into {}
                                        (map (fn [[id s]] [id (vec s)])
                                             selected-roles)))]
                            (on-apply method roles-by-resource)))}]]))

(defn bulk-roles-screen
  [props]
  [:f> bulk-roles-screen-inner props])
