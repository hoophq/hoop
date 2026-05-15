(ns webapp.provisioning.views.bulk-inventory
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Flex Heading
                                Progress Text TextField]]
   ["lucide-react" :refer [AlertCircle CheckCircle2
                            Database Pencil Upload X]]
   ["react" :as react]
   [re-frame.core :as rf]
   [webapp.provisioning.data :as data]
   [webapp.provisioning.views.shared :as shared]))

;; Edits the inventory-level attributes (host, port) of resources that already
;; exist. Structurally mirrors bulk_admin.cljs — same manual/CSV mode toggle,
;; same row-changed?-based apply queue, same progress overlay — but for the
;; HOST/PORT envvars instead of USER/PASS. Identity-class fields (name, type,
;; agent) are intentionally read-only here; the "Add to Inventory" wizard
;; (bulk_import.cljs) owns resource creation, and re-homing to a different
;; agent is a separate, larger concern.

(defn- rows->inventory-attrs
  "Folds parsed CSV rows into {resource-name {:host ... :port ...}}.
   Only the `host` and `port` columns are read; everything else (e.g. `type`)
   is ignored because this flow can't change those identity-class fields."
  [rows]
  (reduce
   (fn [acc row]
     (if (seq (:name row))
       (assoc acc (:name row)
              {:host (str (or (:host row) ""))
               :port (str (or (:port row) ""))})
       acc))
   {}
   rows))

(def ^:private attr-keys [:host :port])

(defn- row-changed?
  "Single source of truth for 'this row will be PUT when Apply is clicked'.
   True iff the current cfg differs from the snapshot taken when the screen
   mounted. Used by both the apply-queue builder and the CSV-preview status
   classifier so badge wording can't drift from behavior."
  [resource cfg initial-cfg]
  (let [id      (:id resource)
        cur     (select-keys (get cfg id) attr-keys)
        initial (select-keys (get initial-cfg id) attr-keys)]
    (not= cur initial)))

(defn- ->queue-item
  "Builds the API payload for a resource whose host/port changed.
   Returns nil when the resource isn't part of the apply set."
  [{:keys [id name] :as resource} cfg initial-cfg]
  (when (row-changed? resource cfg initial-cfg)
    (let [{:keys [host port]} (get cfg id)]
      {:resource-name name
       :host          (or host "")
       :port          (or port "")})))


(def ^:private csv-status-badge
  "Display config for the three CSV-preview row states. There's no `:added`
   here because this flow can't create new resources — only edit existing
   ones — so any CSV row that doesn't match a selected resource is surfaced
   via the unmatched-csv summary chip instead."
  {:updated    {:color "green" :label "Updated"    :icon Pencil
                :row-bg "var(--green-1)"}
   :unchanged  {:color "gray"  :label "Unchanged"  :icon nil
                :row-bg nil}
   :not-in-csv {:color "gray"  :label "Not in CSV" :icon nil
                :row-bg nil}})

(def ^:private status-sort-priority
  "Lower number sorts earlier — green rows surface above the noise."
  {:updated 0 :unchanged 1 :not-in-csv 2})

(defn- csv-row-status
  "Classifies a row in the CSV preview as :updated / :unchanged / :not-in-csv.
   `:not-in-csv` keys off `csv-matched-ids` rather than cfg state so
   pre-filled inventory values don't masquerade as CSV matches."
  [resource cfg initial-cfg csv-matched-ids]
  (cond
    (not (contains? csv-matched-ids (:id resource))) :not-in-csv
    (not (row-changed? resource cfg initial-cfg))    :unchanged
    :else                                            :updated))


(def ^:private bulk-modes
  [{:id "manual" :icon Pencil :label "Edit manually"}
   {:id "csv"    :icon Upload :label "Import from CSV"}])

(defn- mode-toggle
  [{:keys [mode set-mode]}]
  [:> Flex {:gap "2" :mb "4"}
   (for [{:keys [id icon label]} bulk-modes]
     ^{:key id}
     [:> Button {:size "2"
                 :variant (if (= mode id) "solid" "outline")
                 :color   (if (= mode id) "indigo" "gray")
                 :on-click #(set-mode id)}
      [:> icon {:size 14}] (str " " label)])])


(defn- applying-overlay
  "Full-screen overlay shown during and after the apply inventory flow.
   With `results`, renders the success/failure summary; otherwise the spinner
   + progress bar."
  [{:keys [progress changed-count results on-continue]}]
  (if results
    [:> Flex {:direction "column" :align "center" :justify "center"
              :style {:flex 1} :gap "5"}
     [:> Box {:style {:color (if (zero? (:failed results)) "var(--green-9)" "var(--amber-9)")
                      :display "flex"}}
      (if (zero? (:failed results))
        [:> CheckCircle2 {:size 48 :stroke-width 1.5}]
        [:> AlertCircle  {:size 48 :stroke-width 1.5}])]
     [:> Heading {:size "6"} "Inventory update complete"]
     [:> Flex {:direction "column" :gap "1" :align "center"}
      [:> Text {:size "2" :color "green"} (str (:succeeded results) " resources updated")]
      (when (pos? (:failed results))
        [:> Text {:size "2" :color "red"} (str (:failed results) " failed")])]
     [:> Button {:on-click on-continue} "Back to inventory"]]
    [:> Flex {:direction "column" :align "center" :justify "center"
              :style {:flex 1} :gap "5"}
     [:> Flex {:direction "column" :align "center" :gap "4" :style {:width 400}}
      [:> Flex {:align "center" :gap "2"}
       [shared/spinner {:color "indigo" :size 20}]
       [:> Text {:size "3" :weight "medium"}
        (str "Updating inventory\u2026 ("
             (js/Math.round (/ (* progress changed-count) 100))
             " of " changed-count ")")]]
      [:> Box {:style {:width "100%"}}
       [:> Progress {:value progress :size "2" :color "indigo"}]]
      [:> Text {:size "2" :color "gray"} (str (js/Math.round progress) "% complete")]]]))


(defn- manual-inventory-row
  "One editable row in the manual-entry table: resource (read-only) +
   host + port inputs."
  [{:keys [resource cfg-row index total zebra-bg on-host on-port]}]
  [:> Flex {:align "center" :px "3" :py "2"
            :style {:border-bottom (when (< index (dec total)) "1px solid var(--gray-3)")
                    :min-height 52
                    :background zebra-bg}}
   [:> Box {:style {:width 260 :flex-shrink 0}}
    [:> Flex {:align "center" :gap "2"}
     [:> Text {:size "2" :weight "medium"} (:name resource)]
     [:> Badge {:color "gray" :variant "soft" :size "1"} (:db-type resource)]]
    [:> Text {:size "1" :color "gray"
              :style {:font-family "var(--font-mono)" :font-size 11}}
     (:address resource)]]
   [:> Box {:style {:flex 1 :margin-right 12}}
    [:> TextField.Root {:size "1" :placeholder "host.example.com"
                        :value (or (:host cfg-row) "")
                        :onChange #(on-host (.. % -target -value))}]]
   [:> Box {:style {:width 120 :flex-shrink 0}}
    [:> TextField.Root {:size "1" :placeholder "5432"
                        :value (or (:port cfg-row) "")
                        :onChange #(on-port (.. % -target -value))}]]])

(defn- csv-preview-row
  "One read-only row in the CSV preview table. The `:status` keyword drives
   the badge wording, row tint, and whether the host/port cells render the
   live cfg values or an em-dash placeholder (for rows the CSV doesn't touch)."
  [{:keys [resource cfg-row status index total zebra-bg]}]
  (let [{:keys [color label icon row-bg]} (get csv-status-badge status)
        show-attrs? (contains? #{:updated :unchanged} status)]
    [:> Flex {:px "3" :py "2" :align "center"
              :style {:border-bottom (when (< index (dec total)) "1px solid var(--gray-3)")
                      :min-height 44
                      :background (or row-bg zebra-bg)}}
     [:> Flex {:align "center" :gap "2" :style {:flex 1}}
      [:> Text {:size "2" :weight "medium"} (:name resource)]
      [:> Badge {:color "gray" :variant "soft" :size "1"} (:db-type resource)]]
     [:> Box {:style {:width 220 :flex-shrink 0}}
      (if show-attrs?
        [:> Text {:size "2" :style {:font-family "var(--font-mono)" :font-size 12}}
         (or (:host cfg-row) "")]
        [:> Text {:size "1" :color "gray" :style {:font-style "italic"}} "\u2014"])]
     [:> Box {:style {:width 100 :flex-shrink 0}}
      (if show-attrs?
        [:> Text {:size "2" :style {:font-family "var(--font-mono)" :font-size 12}}
         (or (:port cfg-row) "")]
        [:> Text {:size "1" :color "gray" :style {:font-style "italic"}} "\u2014"])]
     [:> Box {:style {:width 110}}
      (if icon
        [:> Badge {:color color :variant "soft" :size "1"}
         [:> icon {:size 10}] (str " " label)]
        [:> Badge {:color color :variant "soft" :size "1"} label])]]))


(defn- manual-mode-body
  [{:keys [resources cfg set-configs]}]
  (let [total (count resources)]
    [:<>
     [shared/flex-table-header
      [{:width 260 :label "Resource"}
       {:flex 1    :label "Host"}
       {:width 120 :label "Port"}]]
     [:> Box {:style {:flex 1 :overflow-y "auto"
                      :border "1px solid var(--gray-5)" :border-top "none"
                      :border-radius "0 0 var(--radius-2) var(--radius-2)"}}
      (for [[i r] (map-indexed vector resources)]
        ^{:key (:id r)}
        [manual-inventory-row
         {:resource r
          :cfg-row  (get cfg (:id r))
          :index    i
          :total    total
          :zebra-bg (shared/zebra-bg i)
          :on-host  (fn [v] (set-configs (fn [c] (assoc-in c [(:id r) :host] v))))
          :on-port  (fn [v] (set-configs (fn [c] (assoc-in c [(:id r) :port] v))))}])]]))

(defn- csv-mode-upload
  [{:keys [on-file]}]
  [:> Flex {:direction "column" :gap "3" :style {:flex 1}}
   [shared/csv-drop-zone {:on-file   on-file
                          :hint-text "Columns: name, host, port"}]])

(defn- csv-summary-strip
  "Compact badge row that summarizes what the uploaded CSV will do.

   `:counts` is a map keyed by status keyword (:updated/:unchanged/:not-in-csv);
   `:unmatched-csv` is the number of CSV rows whose `name` didn't match any
   selected resource (the edit flow won't auto-create those — that's
   bulk-import's job)."
  [{:keys [counts unmatched-csv on-clear]}]
  [:> Flex {:align "center" :gap "3" :mb "2" :wrap "wrap"}
   (when (pos? (:updated counts))
     [:> Badge {:color "green" :variant "soft"}
      [:> Pencil {:size 10}] (str " " (:updated counts) " updated")])
   (when (pos? (:unchanged counts))
     [:> Badge {:color "gray" :variant "soft"}
      (str (:unchanged counts) " unchanged")])
   (when (pos? (:not-in-csv counts))
     [:> Badge {:color "gray" :variant "soft"}
      (str (:not-in-csv counts) " not in CSV")])
   (when (pos? unmatched-csv)
     [:> Badge {:color "amber" :variant "soft"}
      (str unmatched-csv " CSV "
           (if (= 1 unmatched-csv) "row" "rows") " didn't match")])
   [:> Button {:variant "ghost" :size "1" :color "gray" :on-click on-clear}
    [:> X {:size 11}] " Clear"]])

(defn- csv-mode-preview
  [{:keys [resources cfg initial-cfg csv-matched-ids
           csv-match-count csv-row-count on-clear]}]
  (let [classified    (mapv (fn [r]
                              {:resource r
                               :cfg-row  (get cfg (:id r))
                               :status   (csv-row-status r cfg initial-cfg csv-matched-ids)})
                            resources)
        sorted        (vec (sort-by (comp status-sort-priority :status) classified))
        by-status     (group-by :status classified)
        counts        {:updated    (count (:updated    by-status))
                       :unchanged  (count (:unchanged  by-status))
                       :not-in-csv (count (:not-in-csv by-status))}
        unmatched-csv (max 0 (- csv-row-count csv-match-count))
        total         (count sorted)]
    [:> Flex {:direction "column" :gap "3" :style {:flex 1 :min-height 0}}
     [csv-summary-strip {:counts        counts
                         :unmatched-csv unmatched-csv
                         :on-clear      on-clear}]

     (when (zero? csv-match-count)
       [shared/info-callout
        {:color "amber" :size "1" :mb "2"
         :icon  [:> AlertCircle {:size 14}]
         :text  (str "No CSV rows matched the selected resources. "
                     "This flow only edits resources already in inventory \u2014 "
                     "to add new resources, use \"Add to Inventory\".")}])

     [:> Box {:style {:flex 1 :overflow-y "auto"
                      :border "1px solid var(--gray-5)"
                      :border-radius "var(--radius-2)"}}
      [shared/flex-table-header
       [{:flex 1    :label "Resource"}
        {:width 220 :label "Host"}
        {:width 100 :label "Port"}
        {:width 110 :label "Status"}]]
      (for [[i row] (map-indexed vector sorted)]
        ^{:key (:id (:resource row))}
        [csv-preview-row {:resource (:resource row)
                          :cfg-row  (:cfg-row row)
                          :status   (:status row)
                          :index    i
                          :total    total
                          :zebra-bg (shared/zebra-bg i)}])]]))


(defn- bulk-inventory-screen-inner
  [{:keys [resources configs set-configs initial-mode on-cancel on-done]}]
  (let [[mode set-mode]                       (react/useState (or initial-mode "manual"))
        [csv-parsed set-csv-parsed]           (react/useState false)
        [csv-match-count set-csv-match-count] (react/useState 0)
        [csv-row-count set-csv-row-count]     (react/useState 0)
        ;; Set of resource ids whose name appeared in the uploaded CSV. The
        ;; preview badge keys off this rather than cfg state, because
        ;; pre-filled inventory values would otherwise read as CSV matches.
        [csv-matched-ids set-csv-matched-ids] (react/useState #{})
        [applying? set-applying]              (react/useState false)
        [apply-progress set-apply-progress]   (react/useState 0)
        [apply-results set-apply-results]     (react/useState nil)
        [initial-configs _]                   (react/useState configs)
        file-input-ref                        (react/useRef nil)

        cfg                configs
        resource-names     (set (map :name resources))
        resources-by-name  (into {} (map (juxt :name identity) resources))
        queue              (into [] (keep #(->queue-item % cfg initial-configs)) resources)
        changed-count      (count queue)

        handle-csv! (fn [file]
                      (shared/parse-csv!
                       file
                       {:on-complete
                        (fn [rows]
                          (let [by-name     (rows->inventory-attrs rows)
                                matched     (select-keys by-name resource-names)
                                match-count (count matched)
                                matched-ids (into #{}
                                                  (keep #(:id (get resources-by-name %)))
                                                  (keys matched))]
                            (set-csv-row-count   (count rows))
                            (set-csv-match-count match-count)
                            (set-csv-matched-ids matched-ids)
                            (set-csv-parsed      true)
                            (set-configs
                             (fn [old-cfg]
                               (reduce-kv
                                (fn [acc res-name attrs]
                                  (if-let [r (get resources-by-name res-name)]
                                    ;; Merge instead of overwrite — the CSV
                                    ;; may carry just one of host/port; keep
                                    ;; the other from the snapshot.
                                    (update acc (:id r) merge attrs)
                                    acc))
                                old-cfg
                                matched)))))}))
        clear-csv!  (fn []
                      ;; Revert to the pre-CSV snapshot rather than wiping
                      ;; all configs. Mirrors bulk-admin: clearing the CSV
                      ;; should "undo the upload", not "erase what was
                      ;; already configured server-side".
                      (set-csv-parsed false)
                      (set-csv-matched-ids #{})
                      (set-csv-row-count 0)
                      (set-csv-match-count 0)
                      (set-configs (constantly initial-configs))
                      (when-let [el (.-current file-input-ref)]
                        (set! (.-value el) "")))
        do-apply!   (fn []
                      (set-applying true)
                      (set-apply-progress 0)
                      (rf/dispatch
                       [:provisioning/apply-inventory-next
                        {:queue       queue
                         :on-progress (fn [done total]
                                        (set-apply-progress
                                         (js/Math.round (* 100 (/ done total)))))
                         :on-complete (fn [results]
                                        (let [ok   (count (filter #(= :success (:status %)) results))
                                              fail (count (filter #(= :failed (:status %)) results))]
                                          (set-apply-results {:succeeded ok :failed fail})
                                          (set-apply-progress 100)
                                          (rf/dispatch [:provisioning/fetch-resources])))}]))]
    [:> Flex {:direction "column" :style {:flex 1 :min-height 0}}
     [shared/bulk-screen-header {:title          "Inventory \u2014 host & port"
                                 :resource-count (count resources)
                                 :on-back        on-cancel}]

     [shared/info-callout
      {:color "gray" :size "1"
       :icon  [:> Database {:size 14}]
       :text  (str "Only host and port can be edited here. To change a resource's "
                   "name or type, use \"Add to Inventory\"; to re-home an agent, "
                   "edit the connection directly.")}]

     (when applying?
       [applying-overlay {:progress      apply-progress
                          :changed-count changed-count
                          :results       apply-results
                          :on-continue   (or on-done on-cancel)}])

     (when-not applying?
       [:<>
        [:input {:type "file"
                 :accept ".csv"
                 :ref #(set! (.-current file-input-ref) %)
                 :style {:display "none"}
                 :on-change (fn [e]
                              (when-let [file (-> e .-target .-files (aget 0))]
                                (handle-csv! file)))}]

        [mode-toggle {:mode mode :set-mode set-mode}]

        (when (= mode "manual")
          [manual-mode-body {:resources   resources
                             :cfg         cfg
                             :set-configs set-configs}])

        (when (= mode "csv")
          (if-not csv-parsed
            [csv-mode-upload  {:on-file handle-csv!}]
            [csv-mode-preview {:resources       resources
                               :cfg             cfg
                               :initial-cfg     initial-configs
                               :csv-matched-ids csv-matched-ids
                               :csv-match-count csv-match-count
                               :csv-row-count   csv-row-count
                               :on-clear        clear-csv!}]))

        [shared/bulk-footer
         {:info-text       (if (zero? changed-count)
                             (str "No changes \u2014 edit host or port to enable apply ("
                                  (count resources) " resources selected)")
                             (str (data/pluralize changed-count "resource") " changed of "
                                  (count resources) " selected"))
          :on-cancel       on-cancel
          :apply-disabled? (zero? changed-count)
          :apply-label     (str "Apply " (data/pluralize changed-count "change") " \u2192")
          :on-apply        do-apply!}]])]))

(defn bulk-inventory-screen
  [props]
  [:f> bulk-inventory-screen-inner props])
