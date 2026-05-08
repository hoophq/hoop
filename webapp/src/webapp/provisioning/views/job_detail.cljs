(ns webapp.provisioning.views.job-detail
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Flex Heading
                                Progress Table Text]]
   ["lucide-react" :refer [AlertCircle Ban Check CheckCircle2
                            RefreshCw Rocket ScrollText X]]
   [re-frame.core :as rf]
   [webapp.provisioning.data :as data]
   [webapp.provisioning.views.shared :as shared]))

(def ^:private action-icons
  {:x       [:> X {:size 11}]
   :refresh [:> RefreshCw {:size 11}]
   :rocket  [:> Rocket {:size 11}]})

(def ^:private indicator-icons
  {:check [:> CheckCircle2 {:size 14}]
   :ban   [:> Ban {:size 13}]})

(defn- status-indicator [{:keys [color label spinner? icon]}]
  [:> Flex {:align "center" :gap "2"}
   (cond
     spinner? [shared/spinner {:color color :size 13}]
     icon     [:> Box {:style {:color (str "var(--" color "-9)") :display "flex"}}
               (get indicator-icons icon)])
   (if spinner?
     [:> Text {:size "2" :color color} label]
     [:> Badge {:color color :variant "soft" :size "1"} label])])

(defn- status-action [item]
  (when-let [action (get data/plan-item-action (:status item))]
    (let [dispatch-val (get item (:item-key action))
          btn [:> Button {:size "1" :variant (:variant action) :color (:color action)
                          :on-click #(rf/dispatch [(:event action) dispatch-val])}
               (get action-icons (:icon action))
               (when (:label action) (str " " (:label action)))]]
      (if (:cancel? action)
        [:> Flex {:align "center" :gap "1"}
         btn
         [:> Button {:size "1" :variant "ghost" :color "gray"
                     :on-click #(rf/dispatch [:provisioning/cancel-plan-item (:key item)])}
          [:> X {:size 11}]]]
        btn))))

(defn- status-cell [item]
  (let [cfg (get data/plan-item-status (:status item))]
    [:> Flex {:align "center" :gap "2" :justify "between"}
     (if cfg
       [status-indicator cfg]
       [:> Text {:size "2" :color "gray"} (:status item)])
     [status-action item]]))

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

          plan-done       (data/count-by-status items #{"Create" "Update"})
          failed-count    (data/count-by-status items #{"Failed" "ApplyFailed"})
          applied-count   (data/count-by-status items "Applied")
          cancelled-count (data/count-by-status items "Cancelled")
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
        [shared/back-button {:on-click on-back :label "Back to catalog"}]]

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
            (str " " (data/pluralize (count job-sessions) "session"))])
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
         [shared/info-callout {:color "green" :icon [:> CheckCircle2 {:size 16}]
                               :text (str "All " applied-count " roles applied successfully.")}])

       (when (and all-done? (pos? failed-count))
         [shared/info-callout {:color "amber" :icon [:> AlertCircle {:size 16}]
                               :text (str (data/pluralize failed-count "role")
                                          " failed. Use the Retry button to try again.")}])

       (when (and all-planned? (not all-done?) (pos? plan-done) (not applying?))
         [shared/info-callout {:color "blue" :icon [:> Rocket {:size 16}]
                               :text (str "Dry run complete. " (data/pluralize plan-done "role")
                                          " ready to apply. Click 'Apply all' or apply individually.")}])

       (when (and (pos? cancelled-count) (not busy?))
         [shared/info-callout {:color "gray" :icon [:> Ban {:size 16}]
                               :text (str (data/pluralize cancelled-count "role")
                                          " were cancelled.")}])

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
        [shared/back-button {:on-click on-back :label "Back to catalog"}]
        [:> Flex {:gap "2"}
         (when (and all-planned? (pos? plan-done) (not applying?))
           [:> Button {:on-click #(rf/dispatch [:provisioning/apply-all])}
            [:> Rocket {:size 14}]
            (str " Apply " (data/pluralize plan-done "role") " →")])
         (when all-done?
           [:> Button {:color "green" :on-click (or on-done on-back)}
            [:> Check {:size 14}] " Done"])]]])))
