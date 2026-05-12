(ns webapp.provisioning.views.shared
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Callout Flex Heading Text]]
   ["lucide-react" :refer [ArrowLeft CheckCircle2 ChevronLeft ChevronRight
                           Loader2 Upload X]]
   ["papaparse" :as papa]
   ["react" :as react]
   [clojure.string :as cs]))


(defn zebra-bg
  "Returns alternating row background for index i."
  [i]
  (if (even? i) "var(--color-panel-solid)" "var(--gray-1)"))

(defn spinner
  "Animated Loader2 spinner. Wrap any color/size combo from one place."
  ([] (spinner {}))
  ([{:keys [color size] :or {color "indigo" size 13}}]
   [:span {:class "animate-spin inline-flex"
           :style {:color (str "var(--" color "-9)")}}
    [:> Loader2 {:size size}]]))

(defn back-button
  "Ghost back button with leading arrow."
  [{:keys [on-click label size] :or {label "Back" size "2"}}]
  [:> Button {:variant "ghost" :color "gray" :size size :on-click on-click}
   [:> ArrowLeft {:size 14}] (str " " label)])

(defn info-callout
  "Radix Callout with an icon and a text message. Used for status banners."
  [{:keys [color icon text mb size]
    :or   {mb "4"}}]
  [:> Callout.Root (cond-> {:color color :mb mb}
                     size (assoc :size size))
   [:> Callout.Icon icon]
   [:> Callout.Text (if size {:size size} {}) text]])

(defn callout-bar
  "Color-coded banner with a left accent, leading icon, title/subtitle slot,
   and an actions slot on the right."
  [{:keys [color icon title subtitle extra actions px py]
    :or   {px "5" py "4"}}]
  (let [c #(str "var(--" color "-" % ")")]
    [:> Flex {:align "center" :justify "between" :gap "4"
              :px px :py py :mb "4"
              :style {:background    (c 2)
                      :border-top    (str "1px solid " (c 5))
                      :border-right  (str "1px solid " (c 5))
                      :border-bottom (str "1px solid " (c 5))
                      :border-left   (str "4px solid " (c 9))
                      :border-radius "var(--radius-3)"}}
     [:> Flex {:align "center" :gap "3"}
      (when icon
        [:> Box {:style {:color       (c 9)
                         :display     "flex"
                         :flex-shrink 0}}
         icon])
      [:> Flex {:direction "column" :gap "0"}
       (when title    [:> Text {:size "2" :weight "medium"} title])
       (when subtitle [:> Text {:size "1" :color "gray"} subtitle])
       extra]]
     (when actions
       [:> Flex {:align "center" :gap "2" :style {:flex-shrink 0}} actions])]))

(defn bulk-screen-header
  "Back button + heading + resource count badge."
  [{:keys [title resource-count on-back]}]
  [:<>
   [:> Flex {:align "center" :gap "2" :mb "1"}
    [back-button {:on-click on-back}]]
   [:> Flex {:align "baseline" :gap "3" :mb "5"}
    [:> Heading {:size "7"} title]
    [:> Badge {:color "gray" :variant "soft"} (str resource-count " resources")]]])

(defn bulk-footer
  "Sticky footer: info text on the left, Cancel + primary action on the right."
  [{:keys [info-text on-cancel on-apply apply-disabled? apply-label]}]
  [:> Flex {:align "center" :justify "between" :pt "4" :mt "4"
            :style {:border-top "1px solid var(--gray-4)" :flex-shrink 0}}
   [:> Text {:size "1" :color "gray"} info-text]
   [:> Flex {:gap "3"}
    [:> Button {:variant "outline" :color "gray" :on-click on-cancel} "Cancel"]
    [:> Button {:disabled apply-disabled? :on-click on-apply} apply-label]]])

(defn pagination
  "Prev / page-counter / next. Hides itself entirely when there are no pages
   AND no label. With a label, the label still renders on a single page.

   Props:
     :page         (int)  current zero-based page
     :total-pages  (int)
     :on-change    (fn [new-page])
     :label        (string, optional) — left-aligned summary text
     :detail       (string, optional) — extra text shown alongside page counter"
  [{:keys [page total-pages on-change label detail]}]
  (let [paginated? (> total-pages 1)]
    (when (or paginated? label)
      [:> Flex {:align "center" :justify (cond label      "between"
                                                paginated? "center"
                                                :else      "start")
                :gap "3" :py "2"
                :style {:flex-shrink 0}}
       (when label
         [:> Text {:size "1" :color "gray"} label])
       (when paginated?
         [:> Flex {:align "center" :gap "2"}
          [:> Button {:size "1" :variant "ghost" :color "gray"
                      :disabled (zero? page)
                      :on-click #(on-change (dec page))}
           [:> ChevronLeft {:size 14}]]
          [:> Text {:size "1" :color "gray"}
           (str (inc page) " / " total-pages
                (when detail (str " (" detail ")")))]
          [:> Button {:size "1" :variant "ghost" :color "gray"
                      :disabled (>= (inc page) total-pages)
                      :on-click #(on-change (inc page))}
           [:> ChevronRight {:size 14}]]])])))

(defn flex-table-header
  "Sticky header row matching the custom flex-based 'tables' used across views.
   `cols` is a vector of {:flex string|number, :width int, :label string}.
   Either :flex or :width must be set per column."
  [cols]
  [:> Flex {:px "3" :py "2"
            :style {:background "var(--gray-3)"
                    :border-bottom "1px solid var(--gray-5)"
                    :position "sticky" :top 0 :z-index 1}}
   (doall
    (for [[i {:keys [flex width label]}] (map-indexed vector cols)]
      ^{:key i}
      [:> Box {:style (cond-> {}
                       flex  (assoc :flex flex :min-width 0)
                       width (assoc :width width :flex-shrink 0))}
       [:> Text {:size "1" :color "gray" :weight "medium"} label]]))])

(defn- drop-zone-colors
  "Resolves border + background colors for the dropzone based on visual state."
  [{:keys [drag-over? selected?]}]
  (cond
    drag-over? {:border "var(--indigo-7)" :background "var(--indigo-1)"}
    selected?  {:border "var(--green-7)"  :background "var(--green-1)"}
    :else      {:border "var(--gray-6)"   :background "var(--gray-2)"}))

(defn- drop-zone-selected-content
  [{:keys [name detail on-remove]}]
  [:> Flex {:direction "column" :align "center" :gap "2"}
   [:> Box {:style {:color "var(--green-9)" :display "flex"}}
    [:> CheckCircle2 {:size 22 :stroke-width 1.75}]]
   [:> Text {:size "2" :weight "medium"} name]
   (when detail
     [:> Text {:size "1" :color "gray"} detail])
   (when on-remove
     [:> Button {:variant "ghost" :size "1" :color "gray"
                 :on-click (fn [e]
                             (.stopPropagation e)
                             (on-remove))}
      [:> X {:size 11}] " Remove"])])

(defn- drop-zone-empty-content
  [{:keys [hint-text]}]
  [:> Flex {:direction "column" :align "center" :gap "2"}
   [:> Upload {:size 24 :stroke-width 1.5 :color "var(--gray-9)"}]
   [:> Text {:size "2" :color "gray"}
    "Drop your CSV here or "
    [:> Text {:size "2" :color "indigo" :style {:cursor "pointer"}} "browse"]]
   (when hint-text
     [:> Text {:size "1" :color "gray"} hint-text])])

(defn- drop-zone-loading-content
  [{:keys [loading-text]}]
  [:> Flex {:direction "column" :align "center" :gap "2"}
   [spinner {:size 20}]
   [:> Text {:size "2" :color "gray"} (or loading-text "Parsing CSV…")]])

(defn- csv-drop-zone-inner
  [{:keys [on-file hint-text loading? loading-text selected]}]
  (let [[drag-over? set-drag-over] (react/useState false)
        selected?  (some? selected)
        empty?     (and (not selected?) (not loading?))
        {:keys [border background]} (drop-zone-colors {:drag-over? drag-over?
                                                       :selected?  selected?})
        open-picker! (fn []
                       (let [input (js/document.createElement "input")]
                         (set! (.-type input) "file")
                         (set! (.-accept input) ".csv,text/csv")
                         (set! (.-onchange input)
                               (fn [e]
                                 (when-let [file (-> e .-target .-files (aget 0))]
                                   (on-file file))))
                         (.click input)))]
    [:> Box {:on-click      #(when empty? (open-picker!))
             :on-drag-over  (fn [e]
                              (.preventDefault e)
                              (when empty? (set-drag-over true)))
             :on-drag-leave #(set-drag-over false)
             :on-drop       (fn [e]
                              (.preventDefault e)
                              (set-drag-over false)
                              (when empty?
                                (when-let [file (-> e .-dataTransfer .-files (aget 0))]
                                  (on-file file))))
             :style {:border (str "2px dashed " border)
                     :border-radius "var(--radius-3)"
                     :padding 40 :background background
                     :text-align "center"
                     :cursor (if empty? "pointer" "default")
                     :transition "border-color 0.12s ease, background 0.12s ease"
                     :flex 1 :display "flex" :align-items "center"
                     :justify-content "center"}}
     (cond
       loading?  [drop-zone-loading-content  {:loading-text loading-text}]
       selected? [drop-zone-selected-content selected]
       :else     [drop-zone-empty-content    {:hint-text hint-text}])]))

(defn csv-drop-zone
  "Dashed drop zone for CSV file selection. Handles click-to-browse,
   drag-and-drop, and three visual states (empty / loading / selected).

   Props:
     :on-file      (fn [File]) — called when user selects or drops a file
     :hint-text    string      — column description shown when empty
     :loading?     boolean     — show spinner instead of the icon
     :loading-text string      — text shown while loading (default 'Parsing CSV…')
     :selected     map | nil   — when present, render the 'file selected' state:
                                 {:name      string  — file name
                                  :detail    string  — extra details (e.g. row count)
                                  :on-remove fn      — called on Remove click}"
  [props]
  [:f> csv-drop-zone-inner props])


(defn count-csv-rows!
  "Reads `file` as text and passes the non-empty-line count (minus the header
   row) to `on-count`. Used to set up progress totals before a streaming parse."
  [file on-count]
  (let [reader (js/FileReader.)]
    (set! (.-onload reader)
          (fn [e]
            (let [text  (-> e .-target .-result)
                  lines (->> (.split text "\n")
                             (remove #(= "" (.trim %))))]
              (on-count (max 0 (dec (count lines)))))))
    (.readAsText reader file)))

(defn parse-csv!
  "Parses a CSV file via papaparse. Single entry point for both modes:

   Full-parse mode (default):
     (parse-csv! file {:on-complete (fn [rows] ...)})

   Streaming mode (per-row callback for progress reporting):
     (parse-csv! file {:on-row      (fn [row-map row-num] ...)
                       :on-complete (fn [all-rows] ...)})

   `parse-opts` can override papaparse options (e.g. :dynamic-typing? true)."
  [file {:keys [on-row on-complete dynamic-typing?]}]
  (let [collected (atom [])
        n         (atom 0)
        opts      {"header"         true
                   "skipEmptyLines" true
                   "dynamicTyping"  (boolean dynamic-typing?)
                   "complete"
                   (fn [results]
                     (let [rows (if on-row
                                  @collected
                                  (mapv (fn [row]
                                          (js->clj row :keywordize-keys true))
                                        (or (.-data results) [])))]
                       (when on-complete (on-complete rows))))}
        opts      (cond-> opts
                    on-row
                    (assoc "step"
                           (fn [row _parser]
                             (let [row-data (js->clj (.-data row) :keywordize-keys true)
                                   idx      (swap! n inc)]
                               (swap! collected conj row-data)
                               (on-row row-data idx)))))]
    (papa/parse file (clj->js opts))))

;; this is for download an template csv file
;; when the user is in the provisioning bulk he can 
;; download a tamplate file and fill it with the data and upload it 
;; the template already has the resource_name from the resources he has selected
(defn- csv-cell
  "Escapes a cell value per RFC 4180: wraps in quotes when it contains a
   comma, double-quote, or newline; doubles embedded quotes."
  [v]
  (let [s (str (or v ""))]
    (if (re-find #"[\",\n\r]" s)
      (str "\"" (cs/replace s "\"" "\"\"") "\"")
      s)))

(defn build-csv
  "Builds a CSV string from a header vector and a seq of row vectors.
   Each cell is escaped per RFC 4180."
  [header rows]
  (let [encode-row #(cs/join "," (map csv-cell %))]
    (str (encode-row header) "\n"
         (cs/join "\n" (map encode-row rows))
         "\n")))

(defn download-csv!
  "Triggers a browser download of `csv-string` under `filename`."
  [filename csv-string]
  (let [blob (js/Blob. #js [csv-string] #js {:type "text/csv;charset=utf-8"})
        url  (js/URL.createObjectURL blob)
        a    (js/document.createElement "a")]
    (set! (.-href a) url)
    (set! (.-download a) filename)
    (.click a)
    (js/URL.revokeObjectURL url)))
