(ns webapp.provisioning.views.bulk-roles
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Flex Text]]
   ["lucide-react" :refer [AlertTriangle Check ChevronDown
                           ChevronRight Circle FileText Info XCircle]]
   ["react" :as react]
   [clojure.string :as cs]
   [webapp.provisioning.data :as data]
   [webapp.provisioning.views.shared :as shared]))

(def ^:private csv-preview-cols
  [{:flex "1.2 1 0" :label "Resource"}
   {:flex "0.7 1 0" :label "Type"}
   {:flex "0.7 1 0" :label "Role"}
   {:flex "1.2 1 0" :label "Scopes"}
   {:flex "1 1 0"   :label "Permissions"}
   {:width 110      :label "Status"}])

(defn- invalid-reason [row]
  (case (:error row)
    :bad-permissions
    {:badge "Bad permissions"
     :text  "Permissions must be valid SQL grants (SELECT, INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER, CREATE, EXECUTE)"}

    :bad-role-type
    {:badge "Bad role/type"
     :text  "Type must be managed or external, and role must be ro or rw"}

    {:badge "Missing field"
     :text  "All columns are required: resource_name, type, role, scopes, permissions"}))

(def ^:private row-variants
  "Spec for each row type. `:reason` and `:badge-content` may be either a
   literal value or a 1-arg fn of the row, evaluated at render time."
  {:valid     {:badge-color   "green"
               :badge-icon    [:> Check {:size 10}]
               :badge-content "Valid"}
   :duplicate {:badge-color   "gray"
               :badge-icon    [:> Circle {:size 10}]
               :badge-content "Duplicate"
               :bg            "var(--gray-2)"
               :opacity       0.65
               :reason        "Duplicate of line with same resource, type, role, scopes & permissions"}
   :skipped   {:badge-color   "gray"
               :badge-icon    [:> Circle {:size 10}]
               :badge-content "Skipped"
               :bg            "var(--gray-2)"
               :opacity       0.65
               :reason        "Resource not in selection"}
   :invalid   {:badge-color   "red"
               :badge-icon    [:> XCircle {:size 10}]
               :bg            "var(--red-2)"
               :badge-content #(:badge (invalid-reason %))
               :reason        #(:text  (invalid-reason %))}})

(defn- eval-row-field
  "Resolves a variant field that can be either a literal or a 1-arg fn of `row`."
  [v row]
  (if (fn? v) (v row) v))


(defn- csv-row-base [{:keys [row bg extra-style badge-content badge-color badge-icon reason]}]
  (let [dimmed? (contains? #{"gray"} badge-color)]
    [:> Flex {:direction "column"
              :style (merge {:background bg} extra-style)}
     [:> Flex {:px "3" :py "2" :align "center" :style {:min-height 40}}
      [:> Flex {:align "center" :gap "2" :style {:flex "1.2 1 0" :min-width 0}}
       [:> Text {:size "2" :style (when dimmed? {:color "var(--gray-8)"})}
        (:resource-name row)]]
      [:> Box {:style {:flex "0.7 1 0" :min-width 0}}
       (when (seq (:type row))
         [:> Badge {:color (if dimmed? "gray" "gray") :variant "soft" :size "1"
                    :style (when dimmed? {:opacity 0.7})}
          (:type row)])]
      [:> Box {:style {:flex "0.7 1 0" :min-width 0}}
       [:> Text {:size "2" :style {:font-family "var(--font-mono)" :font-size 12
                                   :color (when dimmed? "var(--gray-8)")}}
        (:role row)]]
      [:> Box {:style {:flex "1.2 1 0" :min-width 0}}
       [:> Text {:size "2" :style {:font-family "var(--font-mono)" :font-size 12
                                   :color (when dimmed? "var(--gray-8)")}}
        (:scopes row)]]
      [:> Box {:style {:flex "1 1 0" :min-width 0}}
       [:> Text {:size "1" :color "gray"} (:permissions row)]]
      [:> Box {:style {:width 110 :flex-shrink 0}}
       [:> Badge {:color badge-color :variant "soft" :size "1"}
        badge-icon " " badge-content]]]
     (when reason
       [:> Flex {:px "3" :pb "2" :style {:margin-top -4}}
        [:> Text {:size "1" :style {:color "var(--gray-9)" :font-style "italic"}}
         reason]])]))

(defn- csv-row
  "Renders one CSV row in the given variant style. Looks the variant up in
   `row-variants` and resolves any fn-valued fields against `row`."
  [variant row idx total]
  (let [{:keys [bg opacity badge-color badge-icon badge-content reason]}
        (row-variants variant)]
    [csv-row-base
     {:row           row
      :bg            (or bg (shared/zebra-bg idx))
      :extra-style   (cond-> {:border-bottom (when (< idx (dec total))
                                               "1px solid var(--gray-3)")}
                       opacity (assoc :opacity opacity))
      :badge-color   badge-color
      :badge-icon    badge-icon
      :badge-content (eval-row-field badge-content row)
      :reason        (eval-row-field reason row)}]))

(defn- conflict-radio
  "Amber radio dot used to mark the picked row inside a conflict group."
  [{:keys [active?]}]
  [:> Box {:style {:width 24 :flex-shrink 0 :margin-right 8}}
   [:> Box {:style {:width 16 :height 16 :border-radius "50%"
                    :border (str "2px solid " (if active? "var(--amber-9)" "var(--gray-7)"))
                    :background (when active? "var(--amber-9)")
                    :display "flex" :align-items "center" :justify-content "center"}}
    (when active?
      [:> Box {:style {:width 6 :height 6 :border-radius "50%" :background "white"}}])]])

(defn- conflict-row
  "One selectable row inside a conflict group."
  [{:keys [row picked? dimmed? last? on-pick]}]
  [:> Flex {:px "3" :py "2" :align "center"
            :on-click on-pick
            :style {:min-height 40 :cursor "pointer"
                    :background (if picked? "var(--amber-2)" "var(--gray-2)")
                    :opacity (if dimmed? 0.45 1)
                    :border-bottom (when-not last? "1px solid var(--amber-4)")}}
   [conflict-radio {:active? picked?}]
   [:> Flex {:align "center" :gap "2" :style {:flex "1.2 1 0" :min-width 0}}
    [:> Text {:size "2"} (:resource-name row)]
    [:> Badge {:color "amber" :variant "soft" :size "1"} (str "line " (:line-num row))]]
   [:> Box {:style {:flex "0.7 1 0" :min-width 0}}
    (when (seq (:type row))
      [:> Badge {:color "gray" :variant "soft" :size "1"} (:type row)])]
   [:> Box {:style {:flex "0.7 1 0" :min-width 0}}
    [:> Text {:size "2" :style {:font-family "var(--font-mono)" :font-size 12}}
     (:role row)]]
   [:> Box {:style {:flex "1.2 1 0" :min-width 0}}
    [:> Text {:size "2" :style {:font-family "var(--font-mono)" :font-size 12}}
     (:scopes row)]]
   [:> Box {:style {:flex "1 1 0" :min-width 0}}
    [:> Text {:size "1" :color "gray"} (:permissions row)]]
   [:> Box {:style {:width 110 :flex-shrink 0}}
    [:> Badge {:color "amber" :variant "soft" :size "1"}
     [:> AlertTriangle {:size 10}] " Conflict"]]])

(defn- conflict-group-rows [conflict-id rows picked on-pick]
  (let [total (count rows)]
    [:> Box {:style {:border-left "3px solid var(--amber-8)" :margin-bottom 2}}
     (for [[i row] (map-indexed vector rows)]
       (let [line (:line-num row)]
         ^{:key (str conflict-id "-" line)}
         [conflict-row {:row     row
                        :picked? (= picked line)
                        :dimmed? (and picked (not= picked line))
                        :last?   (= i (dec total))
                        :on-pick #(on-pick line)}]))]))


(defn- skipped-resources-section-inner
  "Collapsible — the badge grid grows linearly with the count, so a thousand
   skipped resources would otherwise push the valid-rows table off-screen.
   Default collapsed; the expanded grid is height-capped with its own scroll
   so it can't dominate the viewport even when the user opens it."
  [{:keys [skipped-resources]}]
  (let [[open? set-open] (react/useState false)
        n                (count skipped-resources)]
    (when (pos? n)
      [:> Box {:mt "3"
               :style {:border "1px solid var(--gray-5)"
                       :border-radius "var(--radius-2)"
                       :background "var(--gray-2)"}}
       [:> Flex {:align "center" :gap "2" :px "3" :py "2"
                 :on-click #(set-open (not open?))
                 :style {:cursor "pointer" :user-select "none"}}
        (if open?
          [:> ChevronDown  {:size 14 :color "var(--gray-9)"}]
          [:> ChevronRight {:size 14 :color "var(--gray-9)"}])
        [:> Info {:size 14 :color "var(--gray-9)"}]
        [:> Text {:size "2" :weight "medium" :color "gray"}
         (str (data/pluralize n "selected resource")
              " had no matching CSV rows")]]
       (when open?
         [:> Box {:px "3" :pb "3"
                  :style {:max-height 160 :overflow-y "auto"}}
          [:> Flex {:gap "2" :style {:flex-wrap "wrap"}}
           (for [r skipped-resources]
             ^{:key (:id r)}
             [:> Badge {:color "gray" :variant "soft" :size "1"} (:name r)])]])])))

(defn- skipped-resources-section [skipped-resources]
  [:f> skipped-resources-section-inner {:skipped-resources skipped-resources}])


(defn- preview-callout [{:keys [has-conflicts? has-invalid?]}]
  (when (or has-conflicts? has-invalid?)
    [shared/info-callout
     {:color (if has-conflicts? "amber" "red")
      :mb    "3"
      :size  "1"
      :icon  (if has-conflicts?
               [:> AlertTriangle {:size 14}]
               [:> XCircle       {:size 14}])
      :text  (cond
               (and has-conflicts? has-invalid?)
               "Some rows have conflicts or errors. Resolve all conflicts (pick one row per group) and fix invalid rows before applying."
               has-conflicts?
               "Some rows have the same resource, type, role, and scopes but different permissions. Pick which row to keep for each conflict group."
               :else
               "Some rows have missing fields, invalid permissions, or invalid type/role values. They will be excluded from provisioning.")}]))

(defn- flat-rows-from-classification
  "Linearises the four bucket vectors into a single tagged stream in display order."
  [{:keys [valid duplicates invalid skipped-csv]}]
  (into []
        (mapcat (fn [[type rows]] (map #(hash-map :type type :row %) rows)))
        [[:valid valid] [:duplicate duplicates] [:invalid invalid] [:skipped skipped-csv]]))

(defn- csv-preview [{:keys [classification conflict-picks set-conflict-picks]}]
  (let [{:keys [conflicts invalid skipped-resources] :as cls} (or classification {})
        has-conflicts? (seq conflicts)
        has-invalid?   (seq invalid)
        flat-rows      (flat-rows-from-classification cls)
        total          (count flat-rows)
        on-pick        (fn [cid line]
                         (set-conflict-picks (assoc conflict-picks cid line)))]
    [:<>
     [preview-callout {:has-conflicts? has-conflicts? :has-invalid? has-invalid?}]

     [:> Box {:style {:flex 1 :overflow-y "auto"
                      :border "1px solid var(--gray-5)"
                      :border-radius "var(--radius-2)"}}
      [shared/flex-table-header csv-preview-cols]

      (into [:<>]
            (when has-conflicts?
              (for [[cid rows] (sort-by key conflicts)]
                ^{:key cid}
                [conflict-group-rows cid rows (get conflict-picks cid) #(on-pick cid %)])))

      (into [:<>]
            (for [[i {:keys [type row]}] (map-indexed vector flat-rows)]
              ^{:key (str (name type) "-" (:line-num row))}
              [csv-row type row i total]))]

     [skipped-resources-section skipped-resources]]))

(defn- normalize-roles-rows
  "Converts raw papa-parsed rows into the role-row shape the classifier expects."
  [rows]
  (vec
   (map-indexed
    (fn [idx row]
      {:resource-name (or (:resource_name row) "")
       :type          (or (:type row) "")
       :role          (or (:role row) "")
       :scopes        (or (:scopes row) "")
       :permissions   (or (:permissions row) "")
       :line-num      (+ idx 2)})
    rows)))

(defn- row->plan-entry
  "Transforms a validated CSV row into the {:type :role :scopes :privileges}
   shape that :provisioning/start-role-plans expects.

   Scopes are strictly `;`-separated (see `data/split-csv-list`); permissions
   accept `,`, `;`, or whitespace so users can mirror SQL grant syntax
   (`SELECT, INSERT, UPDATE`) when authoring the CSV."
  [row]
  {:type       (cs/lower-case (cs/trim (:type row)))
   :role       (cs/lower-case (cs/trim (:role row)))
   :scopes     (data/split-csv-list (:scopes row))
   :privileges (mapv cs/upper-case (data/split-privileges-list (:permissions row)))})

(defn- build-roles-payload
  "Reduces a classification + user's conflict picks into the API shape:
   {resource-id [{:type :role :scopes :privileges} …]}."
  [classification cur-picks resources]
  (let [kept-conflict-rows
        (vec (keep (fn [[cid rows]]
                     (let [ln (get cur-picks cid)]
                       (some #(when (= (:line-num %) ln) %) rows)))
                   (:conflicts classification)))
        all-valid   (concat (:valid classification) kept-conflict-rows)
        by-name     (group-by :resource-name all-valid)
        res-by-name (into {} (map (juxt :name identity)) resources)]
    (into {}
          (keep (fn [[rname rows]]
                  (when-let [r (get res-by-name rname)]
                    [(:id r) (mapv row->plan-entry rows)])))
          by-name)))

(defn- download-template!
  "Generates and downloads a CSV template pre-filled with the selected
   resource names; other columns are left empty for the user to fill."
  [resources]
  (let [rows (->> resources
                  (sort-by :name)
                  (map (fn [r] [(:name r) "" "" "" ""])))
        csv  (shared/build-csv
              ["resource_name" "type" "role" "scopes" "permissions"]
              rows)]
    (shared/download-csv! "hoop-roles-template.csv" csv)))

(defn- count-unresolved-conflicts [classification cur-picks]
  (when classification
    (count (filter (fn [[cid _]] (nil? (get cur-picks cid)))
                   (:conflicts classification)))))

(defn- footer-info-text [classification valid-count]
  (if classification
    (let [conflict-count  (count (:conflicts classification))
          duplicate-count (count (:duplicates classification))
          skipped-count   (+ (count (:skipped-csv       classification))
                             (count (:skipped-resources classification)))]
      (str valid-count " valid"
           (when (pos? duplicate-count)
             (str " \u00b7 " (data/pluralize duplicate-count "duplicate")))
           " \u00b7 " (data/pluralize conflict-count "conflict")
           " \u00b7 " skipped-count " skipped"))
    "Upload a CSV to continue"))

(defn- provision-total
  "Total rows that will be provisioned: valid rows + picked conflict rows."
  [classification cur-picks]
  (+ (count (:valid classification))
     (count (filter #(get cur-picks (key %))
                    (:conflicts (or classification {}))))))

(defn- selected-resources-list
  "Collapsible list of selected resources displayed above the dropzone."
  [{:keys [resources open? toggle]}]
  [:> Box {:style {:border "1px solid var(--gray-5)"
                   :border-radius "var(--radius-2)"
                   :background "var(--gray-2)"}}
   [:> Flex {:align "center" :gap "2" :px "3" :py "2"
             :on-click toggle
             :style {:cursor "pointer" :user-select "none"}}
    (if open?
      [:> ChevronDown  {:size 14 :color "var(--gray-9)"}]
      [:> ChevronRight {:size 14 :color "var(--gray-9)"}])
    [:> Text {:size "2" :weight "medium" :color "gray"}
     (data/pluralize (count resources) "selected resource")]
    [:> Text {:size "1" :color "gray" :style {:margin-left "auto"}}
     "Use these names in the resource_name column"]]
   (when open?
     [:> Flex {:gap "2" :px "3" :pb "3" :style {:flex-wrap "wrap"}}
      (for [r (sort-by :name resources)]
        ^{:key (:id r)}
        [:> Badge {:color "gray" :variant "outline" :size "1"
                   :style {:font-family "var(--font-mono)" :font-size 11}}
         (:name r)])])])

(defn- csv-upload-step
  [{:keys [resources csv-parsing? res-list-open? toggle-res-list
           handle-file! download-template!]}]
  [:> Flex {:direction "column" :gap "3" :style {:flex 1}}
   [selected-resources-list {:resources resources
                             :open?     res-list-open?
                             :toggle    toggle-res-list}]
   [shared/csv-drop-zone {:on-file   handle-file!
                          :hint-text "Columns: resource_name, type, role, scopes, permissions \u2014 separate multiple scopes with ';' (permissions accept ',' or ';')"
                          :loading?  csv-parsing?}]
   [:> Flex {:justify "end"}
    [:> Button {:variant "ghost" :size "1" :color "gray"
                :on-click download-template!}
     [:> FileText {:size 12}] " Download template"]]])

(defn- csv-parsed-step
  [{:keys [csv-parsed classification cur-picks set-conflict-picks clear-csv!]}]
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

(defn- bulk-roles-screen-inner
  [{:keys [resources on-apply on-cancel]}]
  (let [[csv-parsing?   set-csv-parsing]    (react/useState false)
        [csv-parsed     set-csv-parsed]     (react/useState nil)
        [classification set-classification] (react/useState nil)
        [conflict-picks set-conflict-picks] (react/useState {})
        [res-list-open? set-res-list-open]  (react/useState false)

        cur-picks       conflict-picks
        unresolved      (count-unresolved-conflicts classification cur-picks)
        valid-count     (count (:valid classification))
        apply-disabled? (or (nil? classification)
                            (and (seq (:conflicts classification))
                                 (pos? (or unresolved 0))))
        footer-info     (footer-info-text classification valid-count)
        apply-label     (str "Provision " (provision-total classification cur-picks)
                             " roles \u2192")

        handle-file!  (fn [file]
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
        clear-csv!    (fn []
                        (set-csv-parsed nil)
                        (set-classification nil)
                        (set-conflict-picks {}))
        do-apply!     (fn []
                        (when classification
                          (on-apply "csv"
                                    (build-roles-payload classification cur-picks resources))))
        do-template!  (fn [] (download-template! resources))
        toggle-res-list #(set-res-list-open (not res-list-open?))]

    [:> Flex {:direction "column" :style {:flex 1 :min-height 0}}
     [shared/bulk-screen-header {:title          "Provision \u2014 roles"
                                 :resource-count (count resources)
                                 :on-back        on-cancel}]

     (if (nil? csv-parsed)
       [csv-upload-step {:resources          resources
                         :csv-parsing?       csv-parsing?
                         :res-list-open?     res-list-open?
                         :toggle-res-list    toggle-res-list
                         :handle-file!       handle-file!
                         :download-template! do-template!}]
       [csv-parsed-step {:csv-parsed         csv-parsed
                         :classification     classification
                         :cur-picks          cur-picks
                         :set-conflict-picks set-conflict-picks
                         :clear-csv!         clear-csv!}])

     [shared/bulk-footer
      {:info-text       footer-info
       :on-cancel       on-cancel
       :apply-disabled? apply-disabled?
       :apply-label     apply-label
       :on-apply        do-apply!}]]))

(defn bulk-roles-screen
  [props]
  [:f> bulk-roles-screen-inner props])
