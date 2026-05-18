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

(def steps
  [{:key :upload    :label "Source"}
   {:key :parsing   :label "Parsing"}
   {:key :preview   :label "Preview"}
   {:key :importing :label "Importing"}
   {:key :results   :label "Results"}])

(def ^:private step-keys (mapv :key steps))

(def import-status
  {"new"       {:color "green" :label "New"}
   "update"    {:color "blue"  :label "Update"}
   "unchanged" {:color "gray"  :label "Unchanged"}
   "error"     {:color "red"   :label "Error"}})

(defn- step-state
  [i cur-idx]
  (cond
    (< i cur-idx) :done
    (= i cur-idx) :active
    :else         :pending))

(defn- step-segment
  "One step in the wizard indicator: optional leading connector + dot + label."
  [{:keys [index label state]}]
  [:<>
   (when (pos? index)
     [:> Box {:style {:width 18 :height 1
                      :background (if (= :done state) "var(--green-9)" "var(--gray-4)")}}])
   [:> Flex {:align "center" :gap "1"}
    [:> Box {:style {:width 20 :height 20 :border-radius "50%" :flex-shrink 0
                     :background (case state
                                   :done   "var(--green-9)"
                                   :active "var(--indigo-9)"
                                   "var(--gray-4)")
                     :display "flex" :align-items "center" :justify-content "center"}}
     (if (= :done state)
       [:> Check {:size 10 :color "white"}]
       [:> Text {:size "1" :style {:color (if (= :active state) "white" "var(--gray-7)")
                                   :font-size 9 :font-weight 600}}
        (inc index)])]
    [:> Text (cond-> {:size "1"
                      :weight (if (= :active state) "medium" "regular")}
               (= :done state)    (assoc :color "green")
               (= :pending state) (assoc :color "gray"))
     label]]])

(defn step-indicator [current-step]
  (let [cur-idx (.indexOf step-keys current-step)]
    [:> Flex {:align "center" :gap "1"}
     (for [[i s] (map-indexed vector steps)]
       ^{:key (:key s)}
       [step-segment {:index i
                      :label (:label s)
                      :state (step-state i cur-idx)}])]))

(defn- format-file-size [bytes]
  (cond
    (>= bytes 1048576) (str (.toFixed (/ bytes 1048576) 1) " MB")
    (>= bytes 1024)    (str (.toFixed (/ bytes 1024) 1) " KB")
    :else              (str bytes " B")))

(defn- classify-row
  "Returns a classified row map for one CSV row, comparing it to existing resources.
   Result merges a base id-shape with one of five status-specific maps."
  [row-num row existing-by-name]
  (let [name-val (str (or (:name row) ""))
        type-val (str (or (:type row) ""))
        host-val (str (or (:host row) ""))
        port-val (str (or (:port row) ""))
        existing (get existing-by-name name-val)
        ex-host  (str (or (:host existing) ""))
        ex-port  (str (or (:port existing) "5432"))
        base     {:row     row-num
                  :name    name-val
                  :db-type type-val
                  :host    host-val
                  :port    port-val}
        extras   (cond
                   (empty? name-val)
                   {:status "error" :error-reason "missing required field: name"}

                   (empty? type-val)
                   {:status "error" :error-reason "missing required field: type"}

                   (not (#{"PostgreSQL" "postgres"} type-val))
                   {:status "error"
                    :error-reason (str "type not allowed: " type-val
                                       " (only PostgreSQL is supported)")}

                   (and existing (= host-val ex-host) (= port-val ex-port))
                   {:status "unchanged"}

                   existing
                   {:status "update"
                    :update-diff (cond-> []
                                   (not= host-val ex-host)
                                   (conj {:field "host" :from ex-host :to host-val})
                                   (not= port-val ex-port)
                                   (conj {:field "port" :from ex-port :to port-val}))}

                   :else {:status "new"})]
    (merge base extras)))

(defn- summarize-rows
  "Aggregates classified rows into status counts + a flat list of parse errors."
  [classified]
  (let [by-status (group-by :status classified)]
    {:created   (count (get by-status "new"))
     :updated   (count (get by-status "update"))
     :unchanged (count (get by-status "unchanged"))
     :errors    (mapv (fn [e] {:row (:row e) :reason (:error-reason e)})
                      (get by-status "error"))}))

(defn- classify-rows
  "Compares parsed CSV rows against existing resources.
   Returns {:rows [classified-row …] :summary {:created N :updated N :unchanged N :errors [...]}}"
  [parsed-rows existing-resources]
  (let [by-name    (into {} (map (juxt :name identity)) existing-resources)
        classified (into [] (map-indexed #(classify-row (inc %1) %2 by-name)) parsed-rows)]
    {:rows    classified
     :summary (summarize-rows classified)}))


(defn- upload-file
  [{:keys [file-selected file-name* file-size* row-count
           handle-file! clear-file! start-parse!]}]
  [:> Flex {:direction "column" :gap "4"}
   [shared/csv-drop-zone
    {:on-file   handle-file!
     :hint-text "CSV · Columns: name, type, host, port"
     :selected  (when file-selected
                  {:name      file-name*
                   :detail    (str row-count " rows detected \u00b7 "
                                   (format-file-size file-size*))
                   :on-remove clear-file!})}]
   [:> Flex
    [:> Button {:size "2" :disabled (not file-selected)
                :on-click start-parse!}
     "Parse file \u2192"]]])


(def ^:private parsing-checklist
  [[20 "Loaded column headers: name, type, host, port"]
   [50 "Validated required fields on all rows"]
   [70 :dedup]
   [90 "Classifying rows\u2026"]])

(defn- parsing-checklist-line [msg]
  [:> Flex {:align "center" :gap "2"}
   [:> Check {:size 12 :color "var(--green-9)"}]
   [:> Text {:size "1" :color "gray"} msg]])

(defn- parsing-file [{:keys [parse-progress parsed-count total-rows num-existing]}]
  [:> Flex {:direction "column" :align "center" :justify "center"
            :style {:flex 1} :gap "5"}
   [:> Flex {:direction "column" :align "center" :gap "4" :style {:width 380}}
    [:> Flex {:align "center" :gap "2"}
     [shared/spinner {:color "indigo" :size 20}]
     [:> Text {:size "3" :weight "medium"} "Parsing your file\u2026"]]
    [:> Box {:style {:width "100%"}}
     [:> Progress {:value parse-progress :size "2" :color "indigo"}]]
    [:> Text {:size "2" :color "gray"}
     (str "Parsed " parsed-count " of " total-rows
          " rows \u00b7 Validating schema and deduplicating on (name, host)")]
    [:> Flex {:direction "column" :gap "2"
              :style {:width "100%"
                      :opacity (if (> parse-progress 20) 1 0)
                      :transition "opacity 0.3s ease"}}
     (for [[threshold msg] parsing-checklist
           :when (> parse-progress threshold)]
       ^{:key threshold}
       [parsing-checklist-line
        (if (= :dedup msg)
          (str "Deduplication check against " num-existing " existing resources")
          msg)])]]])


(def ^:private page-size 50)

(defn- preview-summary-badges
  [{:keys [total-rows summary error-count]}]
  [:> Flex {:align "center" :gap "3" :wrap "wrap"}
   [:> Heading {:size "5"} (str "Review " total-rows " rows")]
   [:> Badge {:color "green" :variant "soft"} (str (:created summary)   " new")]
   [:> Badge {:color "blue"  :variant "soft"} (str (:updated summary)   " updates")]
   [:> Badge {:color "gray"  :variant "soft"} (str (:unchanged summary) " unchanged")]
   (when (pos? error-count)
     [:> Badge {:color "red" :variant "soft"} (str error-count " error")])])

(def ^:private row-bg
  {"error"  "var(--red-1)"
   "update" "var(--blue-1)"
   "new"    "var(--green-1)"})

(defn- preview-status-cell
  [row]
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
       [:> Text {:size "1" :color "red"} (:error-reason row)])]))

(defn- preview-row [row]
  [:> Table.Row
   {:style {:background (get row-bg (:status row))}}
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
   [:> Table.Cell [preview-status-cell row]]])

(defn- preview-table [rows]
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
     (for [row rows]
       ^{:key (:row row)}
       [preview-row row])]]])

(defn- preview-footer
  [{:keys [summary valid-count error-count on-cancel on-import]}]
  [:> Flex {:align "center" :justify "between" :pt "2" :style {:flex-shrink 0}}
   [:> Text {:size "1" :color "gray"}
    (str (:created summary) " new \u00b7 " (:updated summary)
         " updates \u00b7 " error-count " skipped")]
   [:> Flex {:gap "3"}
    [:> Button {:variant "outline" :color "gray" :on-click on-cancel} "Cancel"]
    [:> Button {:color "indigo"
                :disabled (zero? valid-count)
                :on-click on-import}
     (str "Import " valid-count " rows \u2192")]]])

(defn- summarize-import-results
  "Reduces the per-row callback results into the shape that `results-step`
   expects: {:created N :updated N :failed [row …]}."
  [results]
  (let [succeeded (filterv #(= :success (:status %)) results)
        failed    (filterv #(= :failed  (:status %)) results)
        status-of #(get-in % [:row :status])]
    {:created (count (filter #(= "new"    (status-of %)) succeeded))
     :updated (count (filter #(= "update" (status-of %)) succeeded))
     :failed  failed}))

(defn- preview-file-inner
  [{:keys [on-close set-step set-import-progress set-import-results
           classified-rows summary total-rows]}]
  (let [[page set-page]    (react/useState 0)
        error-count        (count (:errors summary))
        importable-rows    (filterv #(#{"new" "update"} (:status %)) classified-rows)
        valid-count        (count importable-rows)
        total-count        (count classified-rows)
        total-pages        (js/Math.ceil (/ total-count page-size))
        start-idx          (* page page-size)
        end-idx            (min total-count (+ start-idx page-size))
        visible-rows       (subvec (vec classified-rows) start-idx end-idx)
        do-import!         (fn []
                             (set-step :importing)
                             (set-import-progress 0)
                             (rf/dispatch
                              [:provisioning/import-next-resource
                               {:queue       importable-rows
                                :on-progress (fn [done total]
                                               (set-import-progress
                                                (js/Math.round (* 100 (/ done total)))))
                                :on-complete (fn [results]
                                               (set-import-results
                                                (summarize-import-results results))
                                               (rf/dispatch [:provisioning/fetch-resources])
                                               (set-import-progress 100)
                                               (js/setTimeout #(set-step :results) 400))}]))]
    [:> Flex {:direction "column" :gap "3" :style {:flex 1 :min-height 0}}
     [preview-summary-badges {:total-rows  total-rows
                              :summary     summary
                              :error-count error-count}]

     (when (pos? error-count)
       [shared/info-callout
        {:color "amber" :size "1"
         :icon  [:> AlertCircle {:size 14}]
         :text  (str (data/pluralize error-count "row")
                     " will be skipped due to validation errors. "
                     "The remaining " valid-count " rows will be imported.")}])

     [preview-table visible-rows]

     [shared/pagination
      {:page        page
       :total-pages total-pages
       :detail      (str (inc start-idx) "\u2013" end-idx " of " total-count)
       :on-change   set-page}]

     [preview-footer {:summary     summary
                      :valid-count valid-count
                      :error-count error-count
                      :on-cancel   on-close
                      :on-import   do-import!}]]))

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
        (str "Importing resources\u2026 (" done " of " total ")")]]
      [:> Box {:style {:width "100%"}}
       [:> Progress {:value import-progress :size "2" :color "indigo"}]]
      [:> Text {:size "2" :color "gray"}
       (str (js/Math.round import-progress) "% complete"
            (when (pos? (:created summary)) (str " \u00b7 " (:created summary) " new"))
            (when (pos? (:updated summary)) (str " \u00b7 " (:updated summary) " updates")))]]]))


(defn- result-stats-row
  [{:keys [label value color]}]
  [:> Flex {:align "center" :gap "3"}
   [:> Text {:size "2" :color "gray"
             :style {:font-family "var(--font-mono)" :width 110}}
    (str label ":")]
   [:> Text {:size "2" :color color :weight "medium"
             :style {:font-family "var(--font-mono)"}}
    value]])

(defn- result-stats-card
  [{:keys [created updated unchanged parse-errors api-failures]}]
  [:> Box {:style {:width "100%"
                   :border "1px solid var(--gray-5)"
                   :border-radius "var(--radius-3)"
                   :background "var(--gray-1)"
                   :padding "16px 20px"}}
   [:> Flex {:direction "column" :gap "2"}
    (for [{:keys [label] :as stat}
          [{:label "created"      :value created              :color "green"}
           {:label "updated"      :value updated              :color "blue"}
           {:label "skipped"      :value unchanged            :color "gray"}
           {:label "parse errors" :value (count parse-errors) :color "red"}
           {:label "api failures" :value (count api-failures) :color "red"}]]
      ^{:key label}
      [result-stats-row stat])]])

(defn- next-step-row
  "One clickable row in the bottom 'next-step' panel: icon + label + sub-label
   + optional badge. Looks like a list item; click anywhere fires `on-click`."
  [{:keys [icon label sub badge on-click last?]}]
  [:> Flex {:align "center" :gap "3" :px "4" :py "3"
            :style (cond-> {:cursor "pointer"}
                     (not last?) (assoc :border-bottom "1px solid var(--gray-4)"))
            :on-click on-click}
   [:> icon {:size 16 :color (if badge "var(--indigo-9)" "var(--gray-9)")}]
   [:> Flex {:direction "column" :gap "0" :style {:flex 1}}
    [:> Text {:size "2" :weight "medium"} label]
    [:> Text {:size "1" :color "gray"} sub]]
   (when badge
     [:> Badge {:color "indigo" :variant "soft" :size "1"} badge])])

(defn- next-step-rows
  [{:keys [on-view-inventory on-import-another]}]
  (let [items [{:id :inventory :icon Database
                :label "View inventory"
                :sub   "See your updated resource catalog"
                :badge "Recommended"
                :on-click on-view-inventory}
               {:id :again :icon Upload
                :label "Import another file"
                :sub   "Add more resources to the catalog"
                :on-click on-import-another}]
        last-idx (dec (count items))]
    [:> Flex {:direction "column" :gap "0"
              :style {:width "100%"
                      :border "1px solid var(--gray-4)"
                      :border-radius "var(--radius-3)"
                      :overflow "hidden"}}
     (for [[i item] (map-indexed vector items)]
       ^{:key (:id item)}
       [next-step-row (assoc item :last? (= i last-idx))])]))

(defn- results-step [{:keys [on-close clear-file! set-step summary import-results]}]
  (let [parse-errors (:errors summary)
        created      (:created import-results 0)
        updated      (:updated import-results 0)
        api-failures (:failed import-results [])
        all-ok?      (and (zero? (count parse-errors)) (zero? (count api-failures)))
        on-again     (fn [] (set-step :upload) (clear-file!))]
    [:> Flex {:direction "column" :align "center" :justify "center"
              :style {:flex 1} :gap "5"}
     [:> Flex {:direction "column" :align "center" :gap "4"
               :style {:max-width 440 :width "100%"}}
      [:> Box {:style {:color (if all-ok? "var(--green-9)" "var(--amber-9)") :display "flex"}}
       (if all-ok?
         [:> CheckCircle2 {:size 48 :stroke-width 1.5}]
         [:> AlertCircle  {:size 48 :stroke-width 1.5}])]
      [:> Heading {:size "6"} "Import complete"]

      [result-stats-card {:created      created
                          :updated      updated
                          :unchanged    (:unchanged summary 0)
                          :parse-errors parse-errors
                          :api-failures api-failures}]

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

      [next-step-rows {:on-view-inventory on-close
                       :on-import-another on-again}]]]))


(defn bulk-import-screen-inner
  [{:keys [on-close resources]}]
  (let [;; hooks (state + refs)
        [step set-step]                       (react/useState :upload)
        [file-obj set-file-obj]               (react/useState nil)
        [file-name* set-file-name]            (react/useState nil)
        [file-size* set-file-size]            (react/useState 0)
        [row-count set-row-count]             (react/useState 0)
        [classified-rows set-classified-rows] (react/useState [])
        [summary set-summary]                 (react/useState nil)
        [parse-progress set-parse-progress]   (react/useState 0)
        [parsed-count set-parsed-count]       (react/useState 0)
        [import-progress set-import-progress] (react/useState 0)
        [import-results set-import-results]   (react/useState nil)
        row-count-ref                         (react/useRef row-count)

       
        total-rows    row-count
        file-selected (some? file-obj)

        ;; Side-effect: keep ref in sync each render
        _ (set! (.-current row-count-ref) row-count)

        ;; callbacks
        handle-file! (fn [file]
                       (set-file-obj file)
                       (set-file-name (.-name file))
                       (set-file-size (.-size file))
                       (shared/count-csv-rows! file set-row-count))
        clear-file!  (fn []
                       (set-file-obj nil)
                       (set-file-name nil)
                       (set-file-size 0)
                       (set-row-count 0)
                       (set-classified-rows [])
                       (set-summary nil)
                       (set-import-results nil))
        start-parse! (fn []
                       (set-step :parsing)
                       (set-parse-progress 0)
                       (set-parsed-count 0)
                       (shared/parse-csv!
                        file-obj
                        {:dynamic-typing? true
                         :on-row      (fn [_row n]
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
         :upload    [upload-file {:file-selected file-selected
                                  :file-name*    file-name*
                                  :file-size*    file-size*
                                  :row-count     row-count
                                  :handle-file!  handle-file!
                                  :clear-file!   clear-file!
                                  :start-parse!  start-parse!}]
         :parsing   [parsing-file {:parse-progress parse-progress
                                   :parsed-count   parsed-count
                                   :total-rows     total-rows
                                   :num-existing   (count resources)}]
         :preview   [preview-file {:on-close            on-close
                                   :set-step            set-step
                                   :set-import-progress set-import-progress
                                   :set-import-results  set-import-results
                                   :classified-rows     classified-rows
                                   :summary             summary
                                   :total-rows          total-rows}]
         :importing [importing-step {:import-progress import-progress
                                     :summary         summary}]
         :results   [results-step {:on-close       on-close
                                   :clear-file!    clear-file!
                                   :set-step       set-step
                                   :summary        summary
                                   :import-results import-results}])]]]))

(defn bulk-import-screen
  [props]
  [:f> bulk-import-screen-inner props])
