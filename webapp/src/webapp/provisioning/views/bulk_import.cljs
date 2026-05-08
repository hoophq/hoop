(ns webapp.provisioning.views.bulk-import
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Dialog Flex Heading
                               IconButton Progress Table Text]]
   ["react" :as react]
   ["lucide-react" :refer [AlertCircle Check CheckCircle2
                           Database Upload X]]
   [re-frame.core :as rf]
   [webapp.provisioning.data :as data]
   [webapp.provisioning.views.shared :as shared]))

(def step-labels ["Source" "Parsing" "Preview" "Importing" "Results"])
(def step-keys   [:upload :parsing :preview :importing :results])

(def import-status
  {"new"       {:color "green" :label "New"}
   "update"    {:color "blue"  :label "Update"}
   "unchanged" {:color "gray"  :label "Unchanged"}
   "error"     {:color "red"   :label "Error"}})

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
                  existing  (get existing-by-name name-val)
                  ex-host   (str (or (:host existing) ""))
                  ex-port   (str (or (:port existing) "5432"))]
              (cond
                  (or (empty? name-val) (empty? type-val))
                  {:row row-num :name name-val :db-type type-val :host host-val :port port-val
                   :status "error"
                   :error-reason (cond
                                   (empty? name-val) "missing required field: name"
                                   (empty? type-val) "missing required field: type")}

                  (not (#{"PostgreSQL" "postgres"} type-val))
                  {:row row-num :name name-val :db-type type-val :host host-val :port port-val
                   :status "error"
                   :error-reason (str "type not allowed: " type-val " (only PostgreSQL is supported)")}

                  (and existing (= host-val ex-host) (= port-val ex-port))
                  {:row row-num :name name-val :db-type type-val :host host-val :port port-val
                   :status "unchanged"}

                  existing
                  {:row row-num :name name-val :db-type type-val :host host-val :port port-val
                   :status "update"
                   :update-diff (cond-> []
                                  (not= host-val ex-host)
                                  (conj {:field "host" :from ex-host :to host-val})
                                  (not= port-val ex-port)
                                  (conj {:field "port" :from ex-port :to port-val}))}

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
     [shared/spinner {:color "indigo" :size 20}]
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

(def ^:private page-size 50)

(defn- preview-file-inner [{:keys [on-close set-step set-import-progress
                                    set-import-results classified-rows summary total-rows]}]
  (let [[page set-page]    (react/useState 0)
        error-count        (count (:errors summary))
        importable-rows    (filterv #(#{"new" "update"} (:status %)) classified-rows)
        valid-count        (count importable-rows)
        total-count        (count classified-rows)
        total-pages        (js/Math.ceil (/ total-count page-size))
        start-idx          (* page page-size)
        end-idx            (min total-count (+ start-idx page-size))
        visible-rows       (subvec (vec classified-rows) start-idx end-idx)]
    [:> Flex {:direction "column" :gap "3" :style {:flex 1 :min-height 0}}
     [:> Flex {:align "center" :gap "3" :wrap "wrap"}
      [:> Heading {:size "5"} (str "Review " total-rows " rows")]
      [:> Badge {:color "green" :variant "soft"} (str (:created summary) " new")]
      [:> Badge {:color "blue"  :variant "soft"} (str (:updated summary) " updates")]
      [:> Badge {:color "gray"  :variant "soft"} (str (:unchanged summary) " unchanged")]
      (when (pos? error-count)
        [:> Badge {:color "red" :variant "soft"} (str error-count " error")])]

     (when (pos? error-count)
       [shared/info-callout
        {:color "amber" :size "1"
         :icon  [:> AlertCircle {:size 14}]
         :text  (str (data/pluralize error-count "row")
                     " will be skipped due to validation errors. "
                     "The remaining " valid-count " rows will be imported.")}])

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
              (or (:port row) "\u2014")]]
            [:> Table.Cell
             (let [{:keys [color label]} (get import-status (:status row)
                                              {:color "gray" :label (:status row)})]
               [:> Flex {:direction "column" :gap "1"}
                [:> Badge {:color color :variant "soft" :size "1"} label]
                (for [d (:update-diff row)]
                  ^{:key (:field d)}
                  [:> Text {:size "1" :color "gray"
                            :style {:font-family "var(--font-mono)" :font-size 10}}
                   (str (:field d) ": " (:from d) " \u2192 " (:to d))])
                (when (:error-reason row)
                  [:> Text {:size "1" :color "red"} (:error-reason row)])])]]))]]]

     [shared/pagination
      {:page        page
       :total-pages total-pages
       :detail      (str (inc start-idx) "\u2013" end-idx " of " total-count)
       :on-change   set-page}]

     [:> Flex {:align "center" :justify "between" :pt "2" :style {:flex-shrink 0}}
      [:> Text {:size "1" :color "gray"}
       (str (:created summary) " new \u00b7 " (:updated summary) " updates \u00b7 " error-count " skipped")]
      [:> Flex {:gap "3"}
       [:> Button {:variant "outline" :color "gray" :on-click on-close} "Cancel"]
       [:> Button {:color "indigo"
                   :disabled (zero? valid-count)
                   :on-click (fn []
                               (set-step :importing)
                               (set-import-progress 0)
                               (rf/dispatch
                                [:provisioning/import-next-resource
                                 {:queue       importable-rows
                                  :on-progress (fn [done total]
                                                 (set-import-progress
                                                  (js/Math.round (* 100 (/ done total)))))
                                  :on-complete (fn [results]
                                                 (let [succeeded (filterv #(= :success (:status %)) results)
                                                       failed    (filterv #(= :failed (:status %)) results)
                                                       created   (count (filter #(= "new" (get-in % [:row :status])) succeeded))
                                                       updated   (count (filter #(= "update" (get-in % [:row :status])) succeeded))]
                                                   (set-import-results {:created created
                                                                        :updated updated
                                                                        :failed  failed})
                                                   (rf/dispatch [:provisioning/fetch-resources])
                                                   (set-import-progress 100)
                                                   (js/setTimeout #(set-step :results) 400)))}]))}
        (str "Import " valid-count " rows \u2192")]]]]))

(defn- preview-file [props]
  [:f> preview-file-inner props])

(defn- importing-step [{:keys [import-progress summary]}]
  (let [total (+ (:created summary) (:updated summary))
        done  (js/Math.round (/ (* import-progress total) 100))]
    [:> Flex {:direction "column" :align "center" :justify "center"
              :style {:flex 1} :gap "5"}
     [:> Flex {:direction "column" :align "center" :gap "4" :style {:width 400}}
      [:> Flex {:align "center" :gap "2"}
       [shared/spinner {:color "indigo" :size 20}]
       [:> Text {:size "3" :weight "medium"}
        (str "Importing resources… (" done " of " total ")")]]
      [:> Box {:style {:width "100%"}}
       [:> Progress {:value import-progress :size "2" :color "indigo"}]]
      [:> Text {:size "2" :color "gray"}
       (str (js/Math.round import-progress) "% complete"
            (when (pos? (:created summary))
              (str " · " (:created summary) " new"))
            (when (pos? (:updated summary))
              (str " · " (:updated summary) " updates")))]]]))

(defn- results-step [{:keys [on-close clear-file! set-step summary import-results]}]
  (let [parse-errors (:errors summary)
        created      (:created import-results 0)
        updated      (:updated import-results 0)
        api-failures (:failed import-results [])
        all-ok?      (and (zero? (count parse-errors)) (zero? (count api-failures)))]
    [:> Flex {:direction "column" :align "center" :justify "center"
              :style {:flex 1} :gap "5"}
     [:> Flex {:direction "column" :align "center" :gap "4"
               :style {:max-width 440 :width "100%"}}
      [:> Box {:style {:color (if all-ok? "var(--green-9)" "var(--amber-9)") :display "flex"}}
       (if all-ok?
         [:> CheckCircle2 {:size 48 :stroke-width 1.5}]
         [:> AlertCircle {:size 48 :stroke-width 1.5}])]
      [:> Heading {:size "6"} "Import complete"]

      [:> Box {:style {:width "100%"
                       :border "1px solid var(--gray-5)"
                       :border-radius "var(--radius-3)"
                       :background "var(--gray-1)"
                       :padding "16px 20px"}}
       [:> Flex {:direction "column" :gap "2"}
        (for [[k v color] [["created"      created                  "green"]
                           ["updated"      updated                  "blue"]
                           ["skipped"      (:unchanged summary 0)   "gray"]
                           ["parse errors" (count parse-errors)     "red"]
                           ["api failures" (count api-failures)     "red"]]]
          ^{:key k}
          [:> Flex {:align "center" :gap "3"}
           [:> Text {:size "2" :color "gray"
                     :style {:font-family "var(--font-mono)" :width 110}}
            (str k ":")]
           [:> Text {:size "2" :color color :weight "medium"
                     :style {:font-family "var(--font-mono)"}}
            v]])]]

      (when (seq api-failures)
        (let [f       (first api-failures)
              err-msg (or (some-> (:error f) :message) "unknown error")]
          [shared/info-callout
           {:color "red" :size "1" :mb "0"
            :icon  [:> AlertCircle {:size 14}]
            :text  (str "Failed to create \"" (get-in f [:row :name]) "\": " err-msg
                        (when (> (count api-failures) 1)
                          (str " (+" (dec (count api-failures)) " more)")))}]))

      (when (seq parse-errors)
        (let [e (first parse-errors)]
          [shared/info-callout
           {:color "amber" :size "1" :mb "0"
            :icon  [:> AlertCircle {:size 14}]
            :text  (str "Row " (:row e) " skipped: " (:reason e))}]))

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


(defn bulk-import-screen-inner
  [{:keys [on-close resources]}]
  (let [[step set-step]                           (react/useState :upload)
        [file-obj set-file-obj]                   (react/useState nil)
        [file-name* set-file-name]                (react/useState nil)
        [file-size* set-file-size]                (react/useState 0)
        [row-count set-row-count]                 (react/useState 0)
        [classified-rows set-classified-rows]     (react/useState [])
        [summary set-summary]                     (react/useState nil)
        [parse-progress set-parse-progress]       (react/useState 0)
        [parsed-count set-parsed-count]           (react/useState 0)
        [import-progress set-import-progress]     (react/useState 0)
        [import-results set-import-results]       (react/useState nil)
        [drag-over? set-drag-over]                (react/useState false)
        file-input-ref                            (react/useRef nil)
        row-count-ref                             (react/useRef row-count)
        _                                         (set! (.-current row-count-ref) row-count)
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
                      (set-import-results nil)
                      (when-let [el (.-current file-input-ref)]
                        (set! (.-value el) "")))
        start-parse! (fn []
                       (set-step :parsing)
                       (set-parse-progress 0)
                       (set-parsed-count 0)
                       (shared/parse-csv!
                        file-obj
                        {:dynamic-typing? true
                         :on-row     (fn [_row n]
                                       (set-parsed-count n)
                                       (let [total (.-current row-count-ref)]
                                         (when (pos? total)
                                           (set-parse-progress
                                            (min 95 (js/Math.round (* 100 (/ n total))))))))
                         :on-complete (fn [rows]
                                        (let [classified (classify-rows rows resources)]
                                          (set-classified-rows (:rows classified))
                                          (set-summary (:summary classified))
                                          (set-row-count (count rows))
                                          (set-parse-progress 100)
                                          (js/setTimeout #(set-step :preview) 350)))}))]
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
         :preview   (preview-file {:on-close             on-close
                                   :set-step             set-step
                                   :set-import-progress  set-import-progress
                                   :set-import-results   set-import-results
                                   :classified-rows      classified-rows
                                   :summary              summary
                                   :total-rows           total-rows})
         :importing (importing-step {:import-progress import-progress
                                     :summary         summary})
         :results   (results-step {:on-close       on-close
                                   :clear-file!    clear-file!
                                   :set-step       set-step
                                   :summary        summary
                                   :import-results import-results}))]]]))

(defn bulk-import-screen
  [props]
  [:f> bulk-import-screen-inner props])
