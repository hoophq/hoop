(ns webapp.provisioning.views.job-detail
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Callout Flex Heading
                                Progress Table Text]]
   ["lucide-react" :refer [AlertCircle ArrowLeft Ban Check CheckCircle2
                            Loader2 RefreshCw Rocket ScrollText X]]
   [re-frame.core :as rf]))

(defn- status-cell [item]
  (let [status (:status item)]
    [:> Flex {:align "center" :gap "2" :justify "between"}
     ;; Status badge / indicator
     (case status
       "pending"
       [:> Flex {:align "center" :gap "2"}
        [:> Text {:size "2" :color "gray"} "Pending"]
        [:> Button {:size "1" :variant "ghost" :color "gray"
                    :on-click #(rf/dispatch [:provisioning/cancel-plan-item (:key item)])}
         [:> X {:size 11}]]]

       "processing"
       [:> Flex {:align "center" :gap "2"}
        [:span {:class "animate-spin inline-flex"
                :style {:color "var(--indigo-9)"}}
         [:> Loader2 {:size 13}]]
        [:> Text {:size "2" :color "indigo"} "Planning…"]]

       "Create"
       [:> Flex {:align "center" :gap "2"}
        [:> Badge {:color "green" :variant "soft" :size "1"} "Create"]]

       "Update"
       [:> Flex {:align "center" :gap "2"}
        [:> Badge {:color "blue" :variant "soft" :size "1"} "Update"]]

       "Failed"
       [:> Flex {:align "center" :gap "2"}
        [:> Badge {:color "red" :variant "soft" :size "1"} "Failed"]]

       "applying"
       [:> Flex {:align "center" :gap "2"}
        [:span {:class "animate-spin inline-flex"
                :style {:color "var(--indigo-9)"}}
         [:> Loader2 {:size 13}]]
        [:> Text {:size "2" :color "indigo"} "Applying…"]]

       "Applied"
       [:> Flex {:align "center" :gap "2"}
        [:> Box {:style {:color "var(--green-9)" :display "flex"}}
         [:> CheckCircle2 {:size 14}]]
        [:> Badge {:color "green" :variant "soft" :size "1"} "Applied"]]

       "ApplyFailed"
       [:> Flex {:align "center" :gap "2"}
        [:> Badge {:color "red" :variant "soft" :size "1"} "Apply failed"]]

       "Cancelled"
       [:> Flex {:align "center" :gap "2"}
        [:> Box {:style {:color "var(--gray-8)" :display "flex"}}
         [:> Ban {:size 13}]]
        [:> Badge {:color "gray" :variant "soft" :size "1"} "Cancelled"]]

       [:> Text {:size "2" :color "gray"} status])

     ;; Action button
     (case status
       "Failed"
       [:> Button {:size "1" :variant "soft" :color "red"
                   :on-click #(rf/dispatch [:provisioning/retry-plan (:key item)])}
        [:> RefreshCw {:size 11}] " Retry"]

       ("Create" "Update")
       [:> Flex {:align "center" :gap "1"}
        [:> Button {:size "1" :variant "soft" :color "indigo"
                    :on-click #(rf/dispatch [:provisioning/apply-plan (:key item)])}
         [:> Rocket {:size 11}] " Apply"]
        [:> Button {:size "1" :variant "ghost" :color "gray"
                    :on-click #(rf/dispatch [:provisioning/cancel-plan-item (:key item)])}
         [:> X {:size 11}]]]

       "ApplyFailed"
       [:> Button {:size "1" :variant "soft" :color "red"
                   :on-click #(rf/dispatch [:provisioning/apply-plan (:key item)])}
        [:> RefreshCw {:size 11}] " Retry"]

       nil)]))

(defn job-detail-screen
  [_props]
  (fn [{:keys [on-back on-done on-run-in-background on-view-sessions]}]
    (let [plan-job  @(rf/subscribe [:provisioning/plan-job])
          sessions  @(rf/subscribe [:provisioning/sessions])
          items     (or (:items plan-job) [])

          planning?       (some #(contains? #{"pending" "processing"} (:status %)) items)
          applying?       (some #(= "applying" (:status %)) items)
          busy?           (or planning? applying?)
          cancelled?      (:cancelled? plan-job)
          apply-cancelled? (:apply-cancelled? plan-job)

          plan-done       (count (filter #(contains? #{"Create" "Update"} (:status %)) items))
          failed-count    (count (filter #(contains? #{"Failed" "ApplyFailed"} (:status %)) items))
          applied-count   (count (filter #(= "Applied" (:status %)) items))
          cancelled-count (count (filter #(= "Cancelled" (:status %)) items))
          total           (count items)

          terminal?       #(contains? #{"Create" "Update" "Failed" "Applied" "ApplyFailed" "Cancelled"} (:status %))
          all-planned?    (and (pos? total) (not planning?) (every? terminal? items))
          all-done?       (and all-planned?
                               (not applying?)
                               (zero? plan-done))

          progress        (if (pos? total)
                            (let [finished (count (filter terminal? items))]
                              (js/Math.round (* (/ finished total) 100)))
                            0)

          job-sessions    (filterv #(= (:job-id %) (:id plan-job)) sessions)
          session-set     (set (map :id sessions))]

      [:> Flex {:direction "column" :style {:flex 1 :min-height 0}}
       [:> Flex {:align "center" :gap "2" :mb "1"}
        [:> Button {:variant "ghost" :color "gray" :size "2" :on-click on-back}
         [:> ArrowLeft {:size 14}] " Back to catalog"]]

       [:> Flex {:align "center" :justify "between" :mb "4"}
        [:> Flex {:direction "column" :gap "1"}
         [:> Flex {:align "center" :gap "3"}
          [:> Heading {:size "7"} "Provision"]
          (cond
            (and cancelled? (not busy?)) [:> Badge {:color "gray" :variant "soft"} "Cancelled"]
            planning?         [:> Badge {:color "indigo" :variant "soft"} "Planning"]
            applying?         [:> Badge {:color "indigo" :variant "soft"} "Applying"]
            (pos? failed-count) [:> Badge {:color "amber"  :variant "soft"} "Completed with errors"]
            all-done?         [:> Badge {:color "green"  :variant "soft"} "Complete"]
            :else             [:> Badge {:color "blue"   :variant "soft"} "Ready to apply"])]
         [:> Text {:size "2" :color "gray"}
          (str "Role provisioning — " total " roles · "
               applied-count " applied · "
               plan-done " ready · "
               failed-count " failed"
               (when (pos? cancelled-count) (str " · " cancelled-count " cancelled")))]]
        [:> Flex {:align "center" :gap "2"}
         (when (pos? (count job-sessions))
           [:> Button {:size "1" :variant "outline" :color "gray"
                       :on-click #(on-view-sessions nil)}
            [:> ScrollText {:size 13}]
            (str " " (count job-sessions) " session"
                 (when (not= 1 (count job-sessions)) "s"))])
         (when (and planning? (not cancelled?))
           [:> Button {:variant "outline" :color "red" :size "2"
                       :on-click #(rf/dispatch [:provisioning/cancel-plan])}
            [:> X {:size 14}] " Cancel planning"])
         (when (and applying? (not apply-cancelled?))
           [:> Button {:variant "outline" :color "red" :size "2"
                       :on-click #(rf/dispatch [:provisioning/cancel-apply])}
            [:> X {:size 14}] " Cancel applying"])
         (when busy?
           [:> Button {:variant "ghost" :color "gray" :size "2"
                       :on-click on-run-in-background}
            "Background"])]]

       [:> Box {:mb "4"}
        [:> Progress {:value progress
                      :color (cond busy?             "indigo"
                                   (pos? failed-count) "amber"
                                   :else               "green")}]]

       (when (and all-done? (zero? failed-count))
         [:> Callout.Root {:color "green" :mb "4"}
          [:> Callout.Icon [:> CheckCircle2 {:size 16}]]
          [:> Callout.Text
           (str "All " applied-count " roles applied successfully.")]])

       (when (and all-done? (pos? failed-count))
         [:> Callout.Root {:color "amber" :mb "4"}
          [:> Callout.Icon [:> AlertCircle {:size 16}]]
          [:> Callout.Text
           (str failed-count " role" (when (not= 1 failed-count) "s")
                " failed. Use the Retry button to try again.")]])

       (when (and all-planned? (not all-done?) (pos? plan-done) (not applying?))
         [:> Callout.Root {:color "blue" :mb "4"}
          [:> Callout.Icon [:> Rocket {:size 16}]]
          [:> Callout.Text
           (str "Dry run complete. " plan-done " role"
                (when (not= 1 plan-done) "s")
                " ready to apply. Click 'Apply all' or apply individually.")]])

       (when (and (pos? cancelled-count) (not busy?))
         [:> Callout.Root {:color "gray" :mb "4"}
          [:> Callout.Icon [:> Ban {:size 16}]]
          [:> Callout.Text
           (str cancelled-count " role"
                (when (not= 1 cancelled-count) "s")
                " were cancelled.")]])

       [:> Box {:style {:flex 1 :overflow-y "auto"
                        :border "1px solid var(--gray-5)"
                        :border-radius "var(--radius-2)"}}
        [:> Table.Root {:variant "ghost"}
         [:> Table.Header
          [:> Table.Row
           [:> Table.ColumnHeaderCell "Resource"]
           [:> Table.ColumnHeaderCell "Sessions"]
           [:> Table.ColumnHeaderCell "Status"]]]
         [:> Table.Body
          (doall
           (for [item items]
             ^{:key (:key item)}
             [:> Table.Row
              [:> Table.Cell
               [:> Flex {:direction "column" :gap "1"}
                [:> Text {:size "2" :weight "medium"} (:resource-name item)]
                [:> Flex {:align "center" :gap "2"}
                 [:> Text {:size "1" :color "gray"
                           :style {:font-family "var(--font-mono)" :font-size 11}}
                  (:role item)]
                 [:> Text {:size "1" :color "gray"} "·"]
                 [:> Text {:size "1" :color "gray"
                           :style {:font-family "var(--font-mono)" :font-size 11}}
                  (:database item)]]]]
              [:> Table.Cell
               (if (:session-id item)
                 (let [loaded? (contains? session-set (:session-id item))]
                   [:> Button {:size "1" :variant "ghost" :color "indigo"
                               :on-click (fn []
                                           (when-not loaded?
                                             (rf/dispatch [:provisioning/fetch-plan-session
                                                           (:session-id item)]))
                                           (on-view-sessions
                                            {:resource-id   (:resource-id item)
                                             :resource-name (:resource-name item)}))}
                    [:> ScrollText {:size 12}] " View session"])
                 [:> Text {:size "1" :color "gray"} "—"])]
              [:> Table.Cell
               [status-cell item]]]))]]]

       [:> Flex {:align "center" :justify "between" :pt "4" :mt "4"
                 :style {:border-top "1px solid var(--gray-4)" :flex-shrink 0}}
        [:> Button {:variant "outline" :color "gray" :on-click on-back}
         [:> ArrowLeft {:size 14}] " Back to catalog"]
        [:> Flex {:gap "2"}
         (when (and all-planned? (pos? plan-done) (not applying?))
           [:> Button {:on-click #(rf/dispatch [:provisioning/apply-all])}
            [:> Rocket {:size 14}]
            (str " Apply " plan-done " role" (when (not= 1 plan-done) "s") " →")])
         (when all-done?
           [:> Button {:color "green" :on-click (or on-done on-back)}
            [:> Check {:size 14}] " Done"])]]])))
