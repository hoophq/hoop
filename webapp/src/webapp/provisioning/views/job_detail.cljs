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

(def ^:private status-badge-variants
  "Ordered [pred color label] triples for the header badge. First match wins;
   the last entry's pred returns true unconditionally and acts as the default.
   Each pred receives a map with :cancelled? :busy? :planning? :applying?
   :failed-count :all-done?."
  [[#(and (:cancelled? %) (not (:busy? %))) "gray"   "Cancelled"]
   [:planning?                              "indigo" "Planning"]
   [:applying?                              "indigo" "Applying"]
   [#(pos? (:failed-count %))               "amber"  "Completed with errors"]
   [:all-done?                              "green"  "Complete"]
   [(constantly true)                       "blue"   "Ready to apply"]])

(defn- status-badge [state]
  (let [[_ color label] (some (fn [[p :as v]] (when (p state) v)) status-badge-variants)]
    [:> Badge {:color color :variant "soft"} label]))

(defn- action-button
  "Button shape helper for the header action row. Visibility is the caller's
   responsibility — either wrap in `when` or filter with `:when` in a `for`."
  [{:keys [variant color size icon icon-size label on-click]
    :or   {size "2" icon-size 14}}]
  [:> Button {:variant variant :color color :size size :on-click on-click}
   (when icon [:> icon {:size icon-size}])
   (when label (str (when icon " ") label))])

(defn- header-actions
  "Returns the header action specs for the current view state. Each spec has
   a `:visible?` predicate that the render loop filters on, plus the props
   consumed by `action-button`."
  [{:keys [job-sessions planning? cancelled? applying? apply-cancelled? busy?
           on-view-sessions on-run-in-background]}]
  [{:visible? (pos? (count job-sessions))
    :variant  "outline" :color "gray" :size "1"
    :icon     ScrollText :icon-size 13
    :label    (data/pluralize (count job-sessions) "session")
    :on-click #(on-view-sessions nil)}
   {:visible? (and planning? (not cancelled?))
    :variant  "outline" :color "red"
    :icon     X :label "Cancel planning"
    :on-click #(rf/dispatch [:provisioning/cancel-plan])}
   {:visible? (and applying? (not apply-cancelled?))
    :variant  "outline" :color "red"
    :icon     X :label "Cancel applying"
    :on-click #(rf/dispatch [:provisioning/cancel-apply])}
   {:visible? busy?
    :variant  "ghost" :color "gray"
    :label    "Background"
    :on-click on-run-in-background}])

(defn- status-callouts
  "Returns the info-callout specs for the current view state. Each spec carries
   its own `:visible?` predicate; multiple callouts may appear simultaneously."
  [{:keys [all-done? failed-count plan-done applied-count cancelled-count
           all-planned? applying? busy?]}]
  [{:visible? (and all-done? (zero? failed-count))
    :color    "green" :icon CheckCircle2
    :text     (str "All " applied-count " roles applied successfully.")}
   {:visible? (and all-done? (pos? failed-count))
    :color    "amber" :icon AlertCircle
    :text     (str (data/pluralize failed-count "role")
                   " failed. Use the Retry button to try again.")}
   {:visible? (and all-planned? (not all-done?) (pos? plan-done) (not applying?))
    :color    "blue" :icon Rocket
    :text     (str "Dry run complete. " (data/pluralize plan-done "role")
                   " ready to apply. Click 'Apply all' or apply individually.")}
   {:visible? (and (pos? cancelled-count) (not busy?))
    :color    "gray" :icon Ban
    :text     (str (data/pluralize cancelled-count "role")
                   " were cancelled.")}])

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

          planning?       (or (:planning? plan-job)
                              (some #(contains? #{"pending" "processing"} (:status %)) items))
          applying?       (or (:applying? plan-job)
                              (some #(= "applying" (:status %)) items))
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

          apply-done?     #(contains? #{"Applied" "ApplyFailed" "Cancelled"} (:status %))
          progress        (if (pos? total)
                            (if applying?
                              (let [done (count (filter apply-done? items))]
                                (js/Math.round (* (/ done total) 100)))
                              (let [finished (count (filter terminal? items))]
                                (js/Math.round (* (/ finished total) 100))))
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
          [status-badge {:cancelled?   cancelled?
                         :busy?        busy?
                         :planning?    planning?
                         :applying?    applying?
                         :failed-count failed-count
                         :all-done?    all-done?}]]
         [:> Text {:size "2" :color "gray"}
          (str "Role provisioning — " total " roles · "
               applied-count " applied · "
               plan-done " ready · "
               failed-count " failed"
               (when (pos? cancelled-count) (str " · " cancelled-count " cancelled")))]]
        [:> Flex {:align "center" :gap "2"}
         (for [{:keys [visible? label] :as a}
               (header-actions {:job-sessions         job-sessions
                                :planning?            planning?
                                :cancelled?           cancelled?
                                :applying?            applying?
                                :apply-cancelled?     apply-cancelled?
                                :busy?                busy?
                                :on-view-sessions     on-view-sessions
                                :on-run-in-background on-run-in-background})
               :when visible?]
           ^{:key label}
           [action-button (dissoc a :visible?)])]]

       [:> Box {:mb "4"}
        [:> Progress {:value progress
                      :color (cond busy?             "indigo"
                                   (pos? failed-count) "amber"
                                   :else               "green")}]]

       (for [{:keys [visible? color icon text]}
             (status-callouts {:all-done?       all-done?
                               :failed-count    failed-count
                               :plan-done       plan-done
                               :applied-count   applied-count
                               :cancelled-count cancelled-count
                               :all-planned?    all-planned?
                               :applying?       applying?
                               :busy?           busy?})
             :when visible?]
         ^{:key text}
         [shared/info-callout {:color color :icon [:> icon {:size 16}] :text text}])

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
