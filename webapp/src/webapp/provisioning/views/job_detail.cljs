(ns webapp.provisioning.views.job-detail
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Callout Flex Heading
                                Progress Table Text]]
   ["lucide-react" :refer [AlertCircle ArrowLeft Check CheckCircle2
                            Loader2 ScrollText]]))

(defn job-detail-screen
  [_props]
  (fn [{:keys [job sessions on-back on-run-in-background on-view-sessions]}]
    (let [items   (:items job)
          done    (count (filter #(= "done" (:status %)) items))
          failed  (count (filter #(= "failed" (:status %)) items))
          total   (count items)
          running? (< (+ done failed) total)
          progress (js/Math.round (* (/ (+ done failed) total) 100))
          job-sessions (filterv #(= (:job-id %) (:id job)) sessions)]

      [:> Flex {:direction "column" :style {:flex 1 :min-height 0}}
       ;; Back
       [:> Flex {:align "center" :gap "2" :mb "1"}
        [:> Button {:variant "ghost" :color "gray" :size "2" :on-click on-back}
         [:> ArrowLeft {:size 14}] " Back to catalog"]]

       ;; Header
       [:> Flex {:align "center" :justify "between" :mb "4"}
        [:> Flex {:direction "column" :gap "1"}
         [:> Flex {:align "center" :gap "3"}
          [:> Heading {:size "7"}
           (if (= (:type job) :admin-setup) "Manage" "Provision")]
          (cond
            running?     [:> Badge {:color "indigo" :variant "soft"} "Running"]
            (pos? failed) [:> Badge {:color "amber"  :variant "soft"} "Completed with errors"]
            :else         [:> Badge {:color "green"  :variant "soft"} "Complete"])]
         [:> Text {:size "2" :color "gray"}
          (str (:label job) " · "
               (if running?
                 (str (+ done failed) " of " total " processed…")
                 (str done " succeeded · " failed " failed")))]]
        [:> Flex {:align "center" :gap "2"}
         (when (pos? (count job-sessions))
           [:> Button {:size "1" :variant "outline" :color "gray"
                       :on-click #(on-view-sessions nil)}
            [:> ScrollText {:size 13}]
            (str " " (count job-sessions) " session"
                 (when (not= 1 (count job-sessions)) "s"))])
         (when running?
           [:> Button {:variant "outline" :color "gray"
                       :on-click on-run-in-background}
            "Run in background"])]]

       ;; Progress bar
       [:> Box {:mb "4"}
        [:> Progress {:value progress
                      :color (cond running? "indigo"
                                   (pos? failed) "amber"
                                   :else "green")}]]

       ;; Completion callouts
       (when (and (not running?) (pos? failed))
         [:> Callout.Root {:color "amber" :mb "4"}
          [:> Callout.Icon [:> AlertCircle {:size 16}]]
          [:> Callout.Text
           (str failed " resource" (when (not= 1 failed) "s")
                " failed. Review the errors below and retry individually from the catalog.")]])

       (when (and (not running?) (zero? failed))
         [:> Callout.Root {:color "green" :mb "4"}
          [:> Callout.Icon [:> CheckCircle2 {:size 16}]]
          [:> Callout.Text
           (str "All " done " resources processed successfully. "
                "Each step created an auditable session.")]])

       ;; Table
       [:> Box {:style {:flex 1 :overflow-y "auto"
                        :border "1px solid var(--gray-5)"
                        :border-radius "var(--radius-2)"}}
        [:> Table.Root {:variant "ghost"}
         [:> Table.Header
          [:> Table.Row
           [:> Table.ColumnHeaderCell "Resource"]
           [:> Table.ColumnHeaderCell "Type"]
           [:> Table.ColumnHeaderCell "Status"]
           [:> Table.ColumnHeaderCell "Sessions"]]]
         [:> Table.Body
          (doall
           (for [item items]
             (let [item-sessions (filterv #(and (= (:job-id %) (:id job))
                                                (= (:resource-id %) (:resource-id item)))
                                          sessions)]
               ^{:key (:resource-id item)}
               [:> Table.Row
                [:> Table.Cell [:> Text {:size "2" :weight "medium"} (:resource-name item)]]
                [:> Table.Cell [:> Badge {:color "gray" :variant "soft" :size "1"}
                                (:resource-type item)]]
                [:> Table.Cell
                 (case (:status item)
                   "pending" [:> Text {:size "2" :color "gray"} "Pending"]
                   "running" [:> Flex {:align "center" :gap "2"}
                              [:span {:class "animate-spin inline-flex"
                                      :style {:color "var(--indigo-9)"}}
                               [:> Loader2 {:size 13}]]
                              [:> Text {:size "2" :color "indigo"} "Processing…"]]
                   "done"    [:> Flex {:align "center" :gap "2"}
                              [:> Box {:style {:color "var(--green-9)" :display "flex"}}
                               [:> Check {:size 14}]]
                              [:> Text {:size "2" :color "green"} "Done"]]
                   "failed"  [:> Flex {:align "center" :gap "2"}
                              [:> Box {:style {:color "var(--red-9)" :display "flex"}}
                               [:> AlertCircle {:size 14}]]
                              [:> Text {:size "2" :color "red"} "Failed — connection timeout"]])]
                [:> Table.Cell
                 (if (pos? (count item-sessions))
                   [:> Button {:size "1" :variant "ghost" :color "indigo"
                               :on-click #(on-view-sessions
                                           {:resource-id   (:resource-id item)
                                            :resource-name (:resource-name item)})}
                    [:> ScrollText {:size 12}]
                    (str " " (count item-sessions)
                         " session" (when (not= 1 (count item-sessions)) "s"))]
                   [:> Text {:size "1" :color "gray"} "—"])]])))]]

       ;; Done footer
       (when-not running?
         [:> Flex {:justify "end" :pt "4" :mt "4"
                   :style {:border-top "1px solid var(--gray-4)" :flex-shrink 0}}
          [:> Button {:on-click on-back}
           [:> Check {:size 14}] " Back to catalog"]])]])))
