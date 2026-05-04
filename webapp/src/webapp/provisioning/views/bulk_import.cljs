(ns webapp.provisioning.views.bulk-import
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Callout Dialog Flex Heading
                               IconButton Progress Table Text]]
   ["papaparse" :as papa]
   ["react" :as react]
   ["lucide-react" :refer [AlertCircle Check CheckCircle2
                           Database Loader2 Upload X]]))

(def step-labels ["Source" "Parsing" "Preview" "Importing" "Results"])
(def step-keys   [:upload :parsing :preview :importing :results])

(def import-status-color
  {"new"       "green"
   "update"    "blue"
   "unchanged" "gray"
   "error"     "red"})

(def import-status-label
  {"new"       "New"
   "update"    "Update"
   "unchanged" "Unchanged"
   "error"     "Error"})

(defn step-indicator [current-step]
  (let [cur-idx (.indexOf step-keys current-step)]
    [:> Flex {:align "center" :gap "1"}
     (doall
      (for [[i s] (map-indexed vector step-keys)]
        (let [done?   (< i cur-idx)
              active? (= i cur-idx)]
          ^{:key s}
          [:<>
           (when (pos? i)
             [:> Box {:style {:width 18 :height 1
                              :background (if done? "var(--green-9)" "var(--gray-4)")}}])
           [:> Flex {:align "center" :gap "1"}
            [:> Box {:style {:width 20 :height 20 :border-radius "50%" :flex-shrink 0
                             :background (cond done? "var(--green-9)"
                                               active? "var(--indigo-9)"
                                               :else "var(--gray-4)")
                             :display "flex" :align-items "center" :justify-content "center"}}
             (if done?
               [:> Check {:size 10 :color "white"}]
               [:> Text {:size "1" :style {:color (if active? "white" "var(--gray-7)")
                                           :font-size 9 :font-weight 600}}
                (inc i)])]
            [:> Text (cond-> {:size "1"
                              :weight (if active? "medium" "regular")}
                       (and (not active?) done?)       (assoc :color "green")
                       (and (not active?) (not done?)) (assoc :color "gray"))
             (nth step-labels i)]]])))]))

(defn- format-file-size [bytes]
  (cond
    (>= bytes 1048576) (str (.toFixed (/ bytes 1048576) 1) " MB")
    (>= bytes 1024)    (str (.toFixed (/ bytes 1024) 1) " KB")
    :else              (str bytes " B")))

(defn- count-csv-rows
  [file on-count]
  (let [reader (js/FileReader.)]
    (set! (.-onload reader)
          (fn [e]
            (let [text  (-> e .-target .-result)
                  lines (->> (.split text "\n")
                             (remove #(= "" (.trim %))))]
              (on-count (max 0 (dec (count lines)))))))
    (.readAsText reader file)))

(defn- classify-rows
  "Compares parsed CSV rows against existing resources.
   Returns {:rows [...] :summary {:created N :updated N :unchanged N :errors [...]}}"
  [parsed-rows existing-resources]
  (let [existing-by-name (into {} (map (fn [r] [(:name r) r]) existing-resources))
        annotated
        (vec
         (map-indexed
          (fn [i row]
            (let [row-num   (inc i)
                  name-val  (str (or (:name row) ""))
                  type-val  (str (or (:type row) ""))
                  host-val  (str (or (:host row) ""))
                  port-val  (str (or (:port row) ""))
                  existing  (get existing-by-name name-val)]
              (cond
                (or (empty? name-val) (empty? type-val))
                {:row row-num :name name-val :db-type type-val :host host-val :port port-val
                 :status "error"
                 :error-reason (cond
                                 (empty? name-val) "missing required field: name"
                                 (empty? type-val) "missing required field: type")}

                (and existing
                     (= host-val (str (:host existing)))
                     (= port-val (str (or (:port existing) "5432"))))
                {:row row-num :name name-val :db-type type-val :host host-val :port port-val
                 :status "unchanged"}

                existing
                {:row row-num :name name-val :db-type type-val :host host-val :port port-val
                 :status "update"
                 :update-diff (cond-> []
                                (not= host-val (str (:host existing)))
                                (conj {:field "host" :from (:host existing) :to host-val})
                                (not= port-val (str (or (:port existing) "5432")))
                                (conj {:field "port" :from (str (or (:port existing) "5432")) :to port-val}))}

                :else
                {:row row-num :name name-val :db-type type-val :host host-val :port port-val
                 :status "new"})))
          parsed-rows))
        errors (filterv #(= "error" (:status %)) annotated)]
    {:rows    annotated
     :summary {:created   (count (filter #(= "new" (:status %)) annotated))
               :updated   (count (filter #(= "update" (:status %)) annotated))
               :unchanged (count (filter #(= "unchanged" (:status %)) annotated))
               :errors    (mapv (fn [e] {:row (:row e) :reason (:error-reason e)}) errors)}}))

;; ── Sub-components ───────────────────────────────────────────────────────────

(defn- upload-file [{:keys [file-selected file-name* file-size* row-count
                            drag-over? set-drag-over handle-file! clear-file! start-parse! file-input-ref]}]
  [:> Flex {:direction "column" :gap "4" :style {:max-width 560}}
   [:input {:type "file"
            :accept ".csv"
            :ref #(set! (.-current file-input-ref) %)
            :style {:display "none"}
            :on-change (fn [e]
                         (when-let [file (-> e .-target .-files (aget 0))]
                           (handle-file! file)))}]
   [:> Box
    {:on-drag-over  #(do (.preventDefault %) (set-drag-over true))
     :on-drag-leave #(set-drag-over false)
     :on-drop       (fn [e]
                      (.preventDefault e)
                      (set-drag-over false)
                      (when-let [file (-> e .-dataTransfer .-files (aget 0))]
                        (handle-file! file)))
     :on-click      #(when (and (.-current file-input-ref) (not file-selected))
                       (.click (.-current file-input-ref)))
     :style {:border (str "2px dashed "
                          (cond drag-over?     "var(--indigo-7)"
                                file-selected  "var(--green-7)"
                                :else          "var(--gray-6)"))
             :border-radius "var(--radius-3)" :padding 32
             :background (cond drag-over?     "var(--indigo-1)"
                               file-selected  "var(--green-1)"
                               :else          "var(--gray-2)")
             :text-align "center" :cursor "pointer"
             :transition "border-color 0.12s ease, background 0.12s ease"}}
    (if file-selected
      [:> Flex {:direction "column" :align "center" :gap "2"}
       [:> Box {:style {:color "var(--green-9)" :display "flex"}}
        [:> CheckCircle2 {:size 22 :stroke-width 1.75}]]
       [:> Text {:size "2" :weight "medium"} file-name*]
       [:> Text {:size "1" :color "gray"}
        (str row-count " rows detected · " (format-file-size file-size*))]
       [:> Button {:variant "ghost" :size "1" :color "gray"
                   :on-click #(do (.stopPropagation %) (clear-file!))}
        [:> X {:size 11}] " Remove"]]
      [:> Flex {:direction "column" :align "center" :gap "2"}
       [:> Upload {:size 20 :stroke-width 1.75 :color "var(--gray-9)"}]
       [:> Text {:size "2" :color "gray"}
        "Drag your file here or "
        [:> Text {:size "2" :color "indigo" :style {:cursor "pointer"}} "browse"]]
       [:> Text {:size "1" :color "gray"} "CSV · Columns: name, type, host, port"]])]
   [:> Flex
    [:> Button {:size "2" :disabled (not file-selected)
                :on-click start-parse!}
     "Parse file →"]]])

(defn- parsing-file [{:keys [parse-progress parsed-count total-rows num-existing]}]
  [:> Flex {:direction "column" :align "center" :justify "center"
            :style {:flex 1} :gap "5"}
   [:> Flex {:direction "column" :align "center" :gap "4" :style {:width 380}}
    [:> Flex {:align "center" :gap "2"}
     [:> Box {:class "animate-pulse" :style {:color "var(--indigo-9)" :display "flex"}}
      [:> Loader2 {:size 20}]]
     [:> Text {:size "3" :weight "medium"} "Parsing your file…"]]
    [:> Box {:style {:width "100%"}}
     [:> Progress {:value parse-progress :size "2" :color "indigo"}]]
    [:> Text {:size "2" :color "gray"}
     (str "Parsed " parsed-count " of " total-rows
          " rows · Validating schema and deduplicating on (name, host)")]
    [:> Flex {:direction "column" :gap "2"
              :style {:width "100%"
                      :opacity (if (> parse-progress 20) 1 0)
                      :transition "opacity 0.3s ease"}}
     (for [[threshold msg] [[20 "Loaded column headers: name, type, host, port"]
                            [50 "Validated required fields on all rows"]
                            [70 (str "Deduplication check against "
                                     num-existing " existing resources")]
                            [90 "Classifying rows…"]]
           :when (> parse-progress threshold)]
       ^{:key threshold}
       [:> Flex {:align "center" :gap "2"}
        [:> Check {:size 12 :color "var(--green-9)"}]
        [:> Text {:size "1" :color "gray"} msg]])]]])

(defn- preview-file [{:keys [on-confirm on-close set-step set-import-progress set-import-phase
                              classified-rows summary total-rows]}]
  (let [error-count  (count (:errors summary))
        valid-count  (- total-rows error-count)
        visible-rows (take 50 classified-rows)
        hidden-count (max 0 (- (count classified-rows) 50))]
    [:> Flex {:direction "column" :gap "3" :style {:flex 1 :min-height 0}}
     [:> Flex {:align "center" :gap "3" :wrap "wrap"}
      [:> Heading {:size "5"} (str "Review " total-rows " rows")]
      [:> Badge {:color "green" :variant "soft"} (str (:created summary) " new")]
      [:> Badge {:color "blue"  :variant "soft"} (str (:updated summary) " updates")]
      [:> Badge {:color "gray"  :variant "soft"} (str (:unchanged summary) " unchanged")]
      (when (pos? error-count)
        [:> Badge {:color "red" :variant "soft"} (str error-count " error")])]

     (when (pos? error-count)
       [:> Callout.Root {:color "amber" :size "1"}
        [:> Callout.Icon [:> AlertCircle {:size 14}]]
        [:> Callout.Text {:size "1"}
         (str error-count " row" (when (> error-count 1) "s")
              " will be skipped due to validation errors. "
              "The remaining " valid-count " rows will be imported.")]])

     [:> Box {:style {:flex 1 :overflow-y "auto"
                      :border "1px solid var(--gray-5)"
                      :border-radius "var(--radius-2)"}}
      [:> Table.Root
       [:> Table.Header
        [:> Table.Row
         [:> Table.ColumnHeaderCell {:style {:width 44}} "#"]
         [:> Table.ColumnHeaderCell "Name"]
         [:> Table.ColumnHeaderCell "Type"]
         [:> Table.ColumnHeaderCell "Host"]
         [:> Table.ColumnHeaderCell "Port"]
         [:> Table.ColumnHeaderCell "Status"]]]
       [:> Table.Body
        (doall
         (for [row visible-rows]
           ^{:key (:row row)}
           [:> Table.Row
            {:style {:background
                     (case (:status row)
                       "error"  "var(--red-1)"
                       "update" "var(--blue-1)"
                       "new"    "var(--green-1)"
                       nil)}}
            [:> Table.Cell
             [:> Text {:size "1" :color "gray"
                       :style {:font-family "var(--font-mono)"}}
              (:row row)]]
            [:> Table.Cell
             [:> Text {:size "2" :weight "medium"} (:name row)]]
            [:> Table.Cell
             (if (seq (:db-type row))
               [:> Badge {:color "gray" :variant "soft" :size "1"} (:db-type row)]
               [:> Text {:size "1" :color "red" :style {:font-style "italic"}} "missing"])]
            [:> Table.Cell
             [:> Text {:size "2" :style {:font-family "var(--font-mono)" :font-size 12}}
              (:host row)]]
            [:> Table.Cell
             [:> Text {:size "2" :style {:font-family "var(--font-mono)" :font-size 12}}
              (or (:port row) "—")]]
            [:> Table.Cell
             [:> Flex {:direction "column" :gap "1"}
              [:> Badge {:color (get import-status-color (:status row) "gray")
                         :variant "soft" :size "1"}
               (get import-status-label (:status row))]
              (for [d (:update-diff row)]
                ^{:key (:field d)}
                [:> Text {:size "1" :color "gray"
                          :style {:font-family "var(--font-mono)" :font-size 10}}
                 (str (:field d) ": " (:from d) " → " (:to d))])
              (when (:error-reason row)
                [:> Text {:size "1" :color "red"} (:error-reason row)])]]]))

        (when (pos? hidden-count)
          [:> Table.Row
           [:> Table.Cell {:col-span 6 :style {:background "var(--gray-1)"}}
            [:> Text {:size "1" :color "gray" :style {:font-style "italic"}}
             (str "+" hidden-count " more rows")]]])]]]

     [:> Flex {:align "center" :justify "between" :pt "2" :style {:flex-shrink 0}}
      [:> Text {:size "1" :color "gray"}
       (str valid-count " rows will be imported · " error-count " skipped")]
      [:> Flex {:gap "3"}
       [:> Button {:variant "outline" :color "gray" :on-click on-close} "Cancel"]
       [:> Button {:color "indigo"
                   :on-click (fn []
                               (set-step :importing)
                               (set-import-progress 0)
                               (set-import-phase "creating")
                               (let [new-resources (->> classified-rows
                                                        (filter #(= "new" (:status %)))
                                                        (map-indexed
                                                         (fn [i row]
                                                           {:id      (str "imp-" (inc i))
                                                            :name    (:name row)
                                                            :db-type (:db-type row)
                                                            :host    (:host row)
                                                            :stage   :needs-admin}))
                                                        vec)
                                     ticks [[300  #(set-import-progress 12)]
                                            [600  #(set-import-progress 28)]
                                            [900  #(set-import-progress 38)]
                                            [1200 #(do (set-import-progress 40)
                                                       (set-import-phase "updating"))]
                                            [1700 #(set-import-progress 52)]
                                            [2100 #(do (set-import-progress 58)
                                                       (set-import-phase "verifying"))]
                                            [2500 #(set-import-progress 74)]
                                            [2800 #(set-import-progress 90)]
                                            [3100 #(set-import-progress 100)]
                                            [3500 #(do (on-confirm new-resources)
                                                       (set-step :results))]]]
                                 (doseq [[delay f] ticks]
                                   (js/setTimeout f delay))))}
        (str "Import " valid-count " rows →")]]]]))

(defn- importing-step [{:keys [import-progress import-phase summary]}]
  (let [phase-label {"creating"  (str "Creating " (:created summary) " new resources…")
                     "updating"  (str "Updating " (:updated summary) " existing resources…")
                     "verifying" (str "Verifying " (:unchanged summary) " unchanged entries…")}]
    [:> Flex {:direction "column" :align "center" :justify "center"
              :style {:flex 1} :gap "5"}
     [:> Flex {:direction "column" :align "center" :gap "4" :style {:width 400}}
      [:> Flex {:align "center" :gap "2"}
       [:> Box {:class "animate-pulse" :style {:color "var(--indigo-9)" :display "flex"}}
        [:> Loader2 {:size 20}]]
       [:> Text {:size "3" :weight "medium"} (get phase-label import-phase)]]
      [:> Box {:style {:width "100%"}}
       [:> Progress {:value import-progress :size "2" :color "indigo"}]]
      [:> Text {:size "2" :color "gray"} (str (js/Math.round import-progress) "% complete")]]]))

(defn- results-step [{:keys [on-close clear-file! set-step summary]}]
  (let [errors (:errors summary)]
    [:> Flex {:direction "column" :align "center" :justify "center"
              :style {:flex 1} :gap "5"}
     [:> Flex {:direction "column" :align "center" :gap "4"
               :style {:max-width 440 :width "100%"}}
      [:> Box {:style {:color "var(--green-9)" :display "flex"}}
       [:> CheckCircle2 {:size 48 :stroke-width 1.5}]]
      [:> Heading {:size "6"} "Import complete"]

      [:> Box {:style {:width "100%"
                       :border "1px solid var(--gray-5)"
                       :border-radius "var(--radius-3)"
                       :background "var(--gray-1)"
                       :padding "16px 20px"}}
       [:> Flex {:direction "column" :gap "2"}
        (for [[k v color] [["created"   (:created summary)   "green"]
                           ["updated"   (:updated summary)   "blue"]
                           ["unchanged" (:unchanged summary) "gray"]
                           ["errors"    (count errors)       "red"]]]
          ^{:key k}
          [:> Flex {:align "center" :gap "3"}
           [:> Text {:size "2" :color "gray"
                     :style {:font-family "var(--font-mono)" :width 90}}
            (str k ":")]
           [:> Text {:size "2" :color color :weight "medium"
                     :style {:font-family "var(--font-mono)"}}
            v]])
        (when (seq errors)
          [:> Box {:pt "1" :style {:border-top "1px solid var(--gray-4)"}}
           [:> Text {:size "1" :color "gray" :style {:font-family "var(--font-mono)"}}
            (let [e (first errors)]
              (str "errors: [{ row: " (:row e) ", reason: \"" (:reason e) "\" }]"))]])]]

      (when (seq errors)
        [:> Callout.Root {:color "red" :size "1" :style {:width "100%"}}
         [:> Callout.Icon [:> AlertCircle {:size 14}]]
         [:> Callout.Text {:size "1"}
          (let [e (first errors)]
            (str "Row " (:row e) " skipped: " (:reason e)))]])

      [:> Flex {:direction "column" :gap "0"
                :style {:width "100%"
                        :border "1px solid var(--gray-4)"
                        :border-radius "var(--radius-3)"
                        :overflow "hidden"}}
       [:> Flex {:align "center" :gap "3" :px "4" :py "3"
                 :style {:border-bottom "1px solid var(--gray-4)" :cursor "pointer"}
                 :on-click on-close}
        [:> Database {:size 16 :color "var(--indigo-9)"}]
        [:> Flex {:direction "column" :gap "0" :style {:flex 1}}
         [:> Text {:size "2" :weight "medium"} "View inventory"]
         [:> Text {:size "1" :color "gray"} "See your updated resource catalog"]]
        [:> Badge {:color "indigo" :variant "soft" :size "1"} "Recommended"]]
       [:> Flex {:align "center" :gap "3" :px "4" :py "3"
                 :style {:cursor "pointer"}
                 :on-click #(do (set-step :upload)
                                (clear-file!))}
        [:> Upload {:size 16 :color "var(--gray-9)"}]
        [:> Flex {:direction "column" :gap "0" :style {:flex 1}}
         [:> Text {:size "2" :weight "medium"} "Import another file"]
         [:> Text {:size "1" :color "gray"} "Add more resources to the catalog"]]]]]]))

;; ── Main screen ──────────────────────────────────────────────────────────────

(defn bulk-import-screen-inner
  [{:keys [on-confirm on-close resources]}]
  (let [[step set-step]                       (react/useState :upload)
        [file-obj set-file-obj]               (react/useState nil)
        [file-name* set-file-name]            (react/useState nil)
        [file-size* set-file-size]            (react/useState 0)
        [row-count set-row-count]             (react/useState 0)
        [classified-rows set-classified-rows] (react/useState [])
        [summary set-summary]                 (react/useState nil)
        [parse-progress set-parse-progress]   (react/useState 0)
        [parsed-count set-parsed-count]       (react/useState 0)
        [import-progress set-import-progress] (react/useState 0)
        [import-phase set-import-phase]       (react/useState "creating")
        [drag-over? set-drag-over]            (react/useState false)
        file-input-ref                        (react/useRef nil)
        row-count-ref                         (react/useRef row-count)
        _                                     (set! (.-current row-count-ref) row-count)
        total-rows    row-count
        file-selected (some? file-obj)
        handle-file! (fn [file]
                       (set-file-obj file)
                       (set-file-name (.-name file))
                       (set-file-size (.-size file))
                       (count-csv-rows file set-row-count))
        clear-file! (fn []
                      (set-file-obj nil)
                      (set-file-name nil)
                      (set-file-size 0)
                      (set-row-count 0)
                      (set-classified-rows [])
                      (set-summary nil)
                      (when-let [el (.-current file-input-ref)]
                        (set! (.-value el) "")))
        start-parse! (fn []
                       (set-step :parsing)
                       (set-parse-progress 0)
                       (set-parsed-count 0)
                       (let [count-ref (atom 0)
                             rows-ref  (atom [])]
                         (papa/parse file-obj
                                     (clj->js
                                      {"header"         true
                                       "skipEmptyLines" true
                                       "dynamicTyping"  true
                                       "step"           (fn [row _parser]
                                                          (let [row-data (js->clj (.-data row) :keywordize-keys true)]
                                                            (swap! rows-ref conj row-data)
                                                            (swap! count-ref inc)
                                                            (let [n     @count-ref
                                                                  total (.-current row-count-ref)]
                                                              (set-parsed-count n)
                                                              (when (pos? total)
                                                                (set-parse-progress
                                                                 (min 95 (js/Math.round (* 100 (/ n total)))))))))
                                       "complete"       (fn [_results]
                                                          (let [data       @rows-ref
                                                                classified (classify-rows data resources)]
                                                            (set-classified-rows (:rows classified))
                                                            (set-summary (:summary classified))
                                                            (set-row-count (count data))
                                                            (set-parse-progress 100)
                                                            (js/setTimeout #(set-step :preview) 350)))}))))]
    [:> Dialog.Root {:open true
                     :onOpenChange #(when-not % (on-close))}
     [:> Dialog.Content {:max-width "880px"
                         :class "max-h-[90vh] overflow-hidden"}
      [:> Flex {:direction "column" :gap "0"
                :style {:max-height "calc(90vh - 48px)"}}
       [:> Flex {:align "center" :justify "between" :gap "4" :mb "5" :wrap "wrap"}
        [:> Flex {:align "center" :gap "4" :wrap "wrap"}
         [:> Dialog.Title {:asChild true}
          [:> Heading {:as "span" :size "6"} "Import inventory"]]
         [step-indicator step]]
        [:> Dialog.Close {:asChild true}
         [:> IconButton {:variant "ghost" :color "gray" :size "2"
                         :aria-label "Close"}
          [:> X {:size 16}]]]]

       (case step
         :upload    (upload-file {:file-selected  file-selected
                                  :file-name*     file-name*
                                  :file-size*     file-size*
                                  :row-count      row-count
                                  :drag-over?     drag-over?
                                  :set-drag-over  set-drag-over
                                  :handle-file!   handle-file!
                                  :clear-file!    clear-file!
                                  :start-parse!   start-parse!
                                  :file-input-ref file-input-ref})
         :parsing   (parsing-file {:parse-progress parse-progress
                                   :parsed-count   parsed-count
                                   :total-rows     total-rows
                                   :num-existing   (count resources)})
         :preview   (preview-file {:on-confirm           on-confirm
                                   :on-close             on-close
                                   :set-step             set-step
                                   :set-import-progress  set-import-progress
                                   :set-import-phase     set-import-phase
                                   :classified-rows      classified-rows
                                   :summary              summary
                                   :total-rows           total-rows})
         :importing (importing-step {:import-progress import-progress
                                     :import-phase    import-phase
                                     :summary         summary})
         :results   (results-step {:on-close    on-close
                                   :clear-file! clear-file!
                                   :set-step    set-step
                                   :summary     summary}))]]]))

(defn bulk-import-screen
  [props]
  [:f> bulk-import-screen-inner props])
