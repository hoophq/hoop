(ns webapp.provisioning.views.bulk-import
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Callout Flex Heading
                                Progress Table Text]]
   ["lucide-react" :refer [AlertCircle ArrowLeft Check CheckCircle2
                            Database Loader2 Upload X]]
   [reagent.core :as r]
   [webapp.provisioning.data :as data]))

(def total-rows 24)

(def step-labels ["Source" "Parsing" "Preview" "Importing" "Results"])
(def step-keys   [:upload :parsing :preview :importing :results])

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
            [:> Text {:size "1"
                      :color (cond active? nil done? "green" :else "gray")
                      :weight (if active? "medium" "regular")}
             (nth step-labels i)]]])))]))

(defn bulk-import-screen
  [{:keys [on-confirm on-back]}]
  (let [step*           (r/atom :upload)
        file-selected   (r/atom false)
        drag-over       (r/atom false)
        parse-progress  (r/atom 0)
        parsed-count    (r/atom 0)
        import-progress (r/atom 0)
        import-phase    (r/atom "creating")]
    (fn []
      (let [step @step*]
        [:> Flex {:direction "column" :style {:flex 1 :min-height 0}}
         ;; Back
         (when (not= step :results)
           [:> Flex {:align "center" :gap "2" :mb "1"}
            [:> Button {:variant "ghost" :color "gray" :size "2" :on-click on-back}
             [:> ArrowLeft {:size 14}] " Back"]])

         ;; Header + step indicator
         [:> Flex {:align "center" :gap "4" :mb "5" :wrap "wrap"}
          [:> Heading {:size "7"} "Import inventory"]
          [step-indicator step]]

         ;; ── Upload ──
         (when (= step :upload)
           [:> Flex {:direction "column" :gap "4" :style {:max-width 560}}
            [:> Box
             {:on-drag-over  #(do (.preventDefault %) (reset! drag-over true))
              :on-drag-leave #(reset! drag-over false)
              :on-drop       #(do (.preventDefault %) (reset! drag-over false) (reset! file-selected true))
              :on-click      #(when-not @file-selected (reset! file-selected true))
              :style {:border (str "2px dashed "
                                   (cond @drag-over     "var(--indigo-7)"
                                         @file-selected "var(--green-7)"
                                         :else          "var(--gray-6)"))
                      :border-radius "var(--radius-3)" :padding 32
                      :background (cond @drag-over     "var(--indigo-1)"
                                        @file-selected "var(--green-1)"
                                        :else          "var(--gray-2)")
                      :text-align "center" :cursor "pointer"
                      :transition "border-color 0.12s ease, background 0.12s ease"}}
             (if @file-selected
               [:> Flex {:direction "column" :align "center" :gap "2"}
                [:> Box {:style {:color "var(--green-9)" :display "flex"}}
                 [:> CheckCircle2 {:size 22 :stroke-width 1.75}]]
                [:> Text {:size "2" :weight "medium"} "resources-fleet.csv"]
                [:> Text {:size "1" :color "gray"} "24 rows detected · 3.2 KB"]
                [:> Button {:variant "ghost" :size "1" :color "gray"
                            :on-click #(do (.stopPropagation %) (reset! file-selected false))}
                 [:> X {:size 11}] " Remove"]]
               [:> Flex {:direction "column" :align "center" :gap "2"}
                [:> Upload {:size 20 :stroke-width 1.75 :color "var(--gray-9)"}]
                [:> Text {:size "2" :color "gray"}
                 "Drag your file here or "
                 [:> Text {:size "2" :color "indigo" :style {:cursor "pointer"}} "browse"]]
                [:> Text {:size "1" :color "gray"} "CSV, YAML, JSON · Columns: name, type, host, port"]])]

            [:> Flex
             [:> Button {:size "2" :disabled (not @file-selected)
                         :on-click (fn []
                                     (reset! step* :parsing)
                                     (reset! parse-progress 0)
                                     (reset! parsed-count 0)
                                     (let [ticks [150 350 550 750 950 1150 1350 1500]]
                                       (doseq [[i delay] (map-indexed vector ticks)]
                                         (js/setTimeout
                                          (fn []
                                            (reset! parse-progress
                                                    (js/Math.round (* (/ (inc i) (count ticks)) 100)))
                                            (reset! parsed-count
                                                    (js/Math.round (* (/ (inc i) (count ticks)) total-rows)))
                                            (when (= i (dec (count ticks)))
                                              (js/setTimeout #(reset! step* :preview) 350)))
                                          delay))))}
              "Parse file →"]]])

         ;; ── Parsing ──
         (when (= step :parsing)
           [:> Flex {:direction "column" :align "center" :justify "center"
                     :style {:flex 1} :gap "5"}
            [:> Flex {:direction "column" :align "center" :gap "4" :style {:width 380}}
             [:> Flex {:align "center" :gap "2"}
              [:> Box {:class "animate-pulse" :style {:color "var(--indigo-9)" :display "flex"}}
               [:> Loader2 {:size 20}]]
              [:> Text {:size "3" :weight "medium"} "Parsing your file…"]]
             [:> Box {:style {:width "100%"}}
              [:> Progress {:value @parse-progress :size "2" :color "indigo"}]]
             [:> Text {:size "2" :color "gray"}
              (str "Parsed " @parsed-count " of " total-rows
                   " rows · Validating schema and deduplicating on (name, host)")]
             [:> Flex {:direction "column" :gap "2"
                       :style {:width "100%"
                               :opacity (if (> @parse-progress 20) 1 0)
                               :transition "opacity 0.3s ease"}}
              (for [[threshold msg] [[20 "Loaded column headers: name, type, host, port"]
                                     [50 "Validated required fields on all rows"]
                                     [70 (str "Deduplication check against "
                                              (count data/initial-resources) " existing resources")]
                                     [90 (str "Flagged " (count (:errors data/import-result))
                                              " row with validation errors")]]
                    :when (> @parse-progress threshold)]
                ^{:key threshold}
                [:> Flex {:align "center" :gap "2"}
                 [:> Check {:size 12 :color "var(--green-9)"}]
                 [:> Text {:size "1" :color "gray"} msg]])]]])

         ;; ── Preview ──
         (when (= step :preview)
           (let [hidden-unchanged (- (:unchanged data/import-result)
                                     (count (filter #(= "unchanged" (:status %))
                                                    data/mock-import-rows)))]
             [:> Flex {:direction "column" :gap "3" :style {:flex 1 :min-height 0}}
              [:> Flex {:align "center" :gap "3" :wrap "wrap"}
               [:> Heading {:size "5"} (str "Review " total-rows " rows")]
               [:> Badge {:color "green" :variant "soft"} (str (:created data/import-result) " new")]
               [:> Badge {:color "blue"  :variant "soft"} (str (:updated data/import-result) " updates")]
               [:> Badge {:color "gray"  :variant "soft"} (str (:unchanged data/import-result) " unchanged")]
               [:> Badge {:color "red"   :variant "soft"} (str (count (:errors data/import-result)) " error")]]

              [:> Callout.Root {:color "amber" :size "1"}
               [:> Callout.Icon [:> AlertCircle {:size 14}]]
               [:> Callout.Text {:size "1"}
                (str (count (:errors data/import-result)) " row will be skipped due to validation errors. "
                     "The remaining " (- total-rows (count (:errors data/import-result)))
                     " rows will be imported.")]]

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
                  (for [row data/mock-import-rows]
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
                       [:> Badge {:color (get data/import-status-color (:status row) "gray")
                                  :variant "soft" :size "1"}
                        (get data/import-status-label (:status row))]
                       (for [d (:update-diff row)]
                         ^{:key (:field d)}
                         [:> Text {:size "1" :color "gray"
                                   :style {:font-family "var(--font-mono)" :font-size 10}}
                          (str (:field d) ": " (:from d) " → " (:to d))])
                       (when (:error-reason row)
                         [:> Text {:size "1" :color "red"} (:error-reason row)])]]]))

                 (when (pos? hidden-unchanged)
                   [:> Table.Row
                    [:> Table.Cell {:col-span 6 :style {:background "var(--gray-1)"}}
                     [:> Text {:size "1" :color "gray" :style {:font-style "italic"}}
                      (str "+" hidden-unchanged " more rows — all unchanged")]]])]]]

              [:> Flex {:align "center" :justify "between" :pt "2" :style {:flex-shrink 0}}
               [:> Text {:size "1" :color "gray"}
                (str (- total-rows (count (:errors data/import-result)))
                     " rows will be imported · "
                     (count (:errors data/import-result)) " skipped")]
               [:> Flex {:gap "3"}
                [:> Button {:variant "outline" :color "gray" :on-click on-back} "Cancel"]
                [:> Button {:color "indigo"
                            :on-click (fn []
                                        (reset! step* :importing)
                                        (reset! import-progress 0)
                                        (reset! import-phase "creating")
                                        (let [ticks [[300  #(reset! import-progress 12)]
                                                     [600  #(reset! import-progress 28)]
                                                     [900  #(reset! import-progress 38)]
                                                     [1200 #(do (reset! import-progress 40)
                                                                (reset! import-phase "updating"))]
                                                     [1700 #(reset! import-progress 52)]
                                                     [2100 #(do (reset! import-progress 58)
                                                                (reset! import-phase "verifying"))]
                                                     [2500 #(reset! import-progress 74)]
                                                     [2800 #(reset! import-progress 90)]
                                                     [3100 #(reset! import-progress 100)]
                                                     [3500 #(do (on-confirm data/new-resources-from-import)
                                                                (reset! step* :results))]]]
                                          (doseq [[delay f] ticks]
                                            (js/setTimeout f delay))))}
                 (str "Import " (- total-rows (count (:errors data/import-result))) " rows →")]]]]))

         ;; ── Importing ──
         (when (= step :importing)
           (let [phase-label {"creating"  (str "Creating " (:created data/import-result) " new resources…")
                              "updating"  (str "Updating " (:updated data/import-result) " existing resources…")
                              "verifying" (str "Verifying " (:unchanged data/import-result) " unchanged entries…")}]
             [:> Flex {:direction "column" :align "center" :justify "center"
                       :style {:flex 1} :gap "5"}
              [:> Flex {:direction "column" :align "center" :gap "4" :style {:width 400}}
               [:> Flex {:align "center" :gap "2"}
                [:> Box {:class "animate-pulse" :style {:color "var(--indigo-9)" :display "flex"}}
                 [:> Loader2 {:size 20}]]
                [:> Text {:size "3" :weight "medium"} (get phase-label @import-phase)]]
               [:> Box {:style {:width "100%"}}
                [:> Progress {:value @import-progress :size "2" :color "indigo"}]]
               [:> Text {:size "2" :color "gray"} (str (js/Math.round @import-progress) "% complete")]]]))

         ;; ── Results ──
         (when (= step :results)
           [:> Flex {:direction "column" :align "center" :justify "center"
                     :style {:flex 1} :gap "5"}
            [:> Flex {:direction "column" :align "center" :gap "4"
                      :style {:max-width 440 :width "100%"}}
             [:> Box {:style {:color "var(--green-9)" :display "flex"}}
              [:> CheckCircle2 {:size 48 :stroke-width 1.5}]]
             [:> Heading {:size "6"} "Import complete"]

             ;; Response payload
             [:> Box {:style {:width "100%"
                              :border "1px solid var(--gray-5)"
                              :border-radius "var(--radius-3)"
                              :background "var(--gray-1)"
                              :padding "16px 20px"}}
              [:> Flex {:direction "column" :gap "2"}
               (for [[k v color] [["created"   (:created data/import-result)   "green"]
                                   ["updated"   (:updated data/import-result)   "blue"]
                                   ["unchanged" (:unchanged data/import-result) "gray"]
                                   ["errors"    (count (:errors data/import-result)) "red"]]]
                 ^{:key k}
                 [:> Flex {:align "center" :gap "3"}
                  [:> Text {:size "2" :color "gray"
                            :style {:font-family "var(--font-mono)" :width 90}}
                   (str k ":")]
                  [:> Text {:size "2" :color color :weight "medium"
                            :style {:font-family "var(--font-mono)"}}
                   v]])
               [:> Box {:pt "1" :style {:border-top "1px solid var(--gray-4)"}}
                [:> Text {:size "1" :color "gray" :style {:font-family "var(--font-mono)"}}
                 (let [e (first (:errors data/import-result))]
                   (str "errors: [{ row: " (:row e) ", reason: \"" (:reason e) "\" }]"))]]]]

             ;; Error callout
             [:> Callout.Root {:color "red" :size "1" :style {:width "100%"}}
              [:> Callout.Icon [:> AlertCircle {:size 14}]]
              [:> Callout.Text {:size "1"}
               (let [e (first (:errors data/import-result))]
                 (str "Row " (:row e) " skipped: " (:reason e)))]]

             ;; Actions
             [:> Flex {:direction "column" :gap "0"
                       :style {:width "100%"
                               :border "1px solid var(--gray-4)"
                               :border-radius "var(--radius-3)"
                               :overflow "hidden"}}
              [:> Flex {:align "center" :gap "3" :px "4" :py "3"
                        :style {:border-bottom "1px solid var(--gray-4)" :cursor "pointer"}
                        :on-click on-back}
               [:> Database {:size 16 :color "var(--indigo-9)"}]
               [:> Flex {:direction "column" :gap "0" :style {:flex 1}}
                [:> Text {:size "2" :weight "medium"} "View inventory"]
                [:> Text {:size "1" :color "gray"} "See your updated resource catalog"]]
               [:> Badge {:color "indigo" :variant "soft" :size "1"} "Recommended"]]
              [:> Flex {:align "center" :gap "3" :px "4" :py "3"
                        :style {:cursor "pointer"}
                        :on-click #(do (reset! step* :upload)
                                       (reset! file-selected false))}
               [:> Upload {:size 16 :color "var(--gray-9)"}]
               [:> Flex {:direction "column" :gap "0" :style {:flex 1}}
                [:> Text {:size "2" :weight "medium"} "Import another file"]
                [:> Text {:size "1" :color "gray"} "Add more resources to the catalog"]]]]]])]))))
