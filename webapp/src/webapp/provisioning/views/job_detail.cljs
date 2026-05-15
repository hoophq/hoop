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
  "Button shape helper for the header / footer action rows. Visibility is the
   caller's responsibility — either wrap in `when` or filter with `:when` in
   a `for`.

   Optional props (`:variant`, `:color`) are only included when non-nil:
   Radix Themes Button runs every prop through a responsive-object check
   that calls `Object.keys(value)`; since `typeof null === \"object\"` in JS,
   passing `nil` would throw `Cannot convert undefined or null to object`."
  [{:keys [variant color size icon icon-size label on-click]
    :or   {size "2" icon-size 14}}]
  [:> Button (cond-> {:size size :on-click on-click}
               variant (assoc :variant variant)
               color   (assoc :color   color))
   (when icon [:> icon {:size icon-size}])
   (when label (str (when icon " ") label))])

(defn- header-actions
  "Returns the header action specs for the current view state. Each spec has
   a `:visible?` predicate that the render loop filters on, plus the props
   consumed by `action-button`."
  [{:keys [job-sessions-count planning? cancelled? applying? apply-cancelled? busy?
           on-view-sessions on-run-in-background]}]
  ;; Sessions list is hidden while an apply is in progress — exposing
  ;; mid-apply, partially-streamed sessions would let the user open
  ;; rows whose output is still being written by the agent. Once the
  ;; apply finishes (all items terminal), the button reappears.
  [{:visible? (and (pos? job-sessions-count) (not applying?))
    :variant  "outline" :color "gray" :size "1"
    :icon     ScrollText :icon-size 13
    :label    (data/pluralize job-sessions-count "session")
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

(defn- footer-actions
  "Action specs for the footer row. Same DSL as `header-actions`.

   When the run finishes, the confirm button always clears the plan-job
   through `on-done`. Its label/color shifts based on outcome so a green
   'Done' check isn't shown next to a sea of failed rows."
  [{:keys [all-planned? plan-done applying? all-done? failed-count
           applied-count skipped-count on-done on-back]}]
  ;; "Nothing accomplished" means no roles ended up at desired state from
  ;; this run. Skipped roles count toward accomplishment (they were left
  ;; in sync) just like applied roles, so a run of all-skipped + one
  ;; failure is amber ("partial"), not gray ("nothing happened").
  (let [any-failed? (pos? failed-count)
        nothing-ok? (zero? (+ applied-count skipped-count))
        [color label]
        (cond
          (and any-failed? nothing-ok?)   ["gray"  "Dismiss results"]
          any-failed?                     ["amber" "Dismiss results"]
          :else                           ["green" "Done"])]
    [{:visible? (and all-planned? (pos? plan-done) (not applying?))
      :icon     Rocket
      :label    (str "Apply " (data/pluralize plan-done "role") " \u2192")
      :on-click #(rf/dispatch [:provisioning/apply-all])}
     {:visible? all-done?
      :color    color :icon Check :label label
      :on-click (or on-done on-back)}]))

(defn- status-callouts
  "Returns the info-callout specs for the current view state. Each spec carries
   its own `:visible?` predicate; multiple callouts may appear simultaneously."
  [{:keys [all-done? failed-count plan-done applied-count skipped-count
           cancelled-count all-planned? applying? busy?]}]
  ;; `applied-count` counts new SQL runs ("success") and `skipped-count`
  ;; counts roles the agent re-checked and found already in sync. Both
  ;; leave the role at the desired state, so the celebratory message
  ;; folds them together.
  [{:visible? (and all-done? (zero? failed-count))
    :color    "green" :icon CheckCircle2
    :text     (let [total-ok (+ applied-count skipped-count)]
                (str "All " total-ok " roles in sync"
                     (when (pos? skipped-count)
                       (str " (" skipped-count " already up to date)"))
                     "."))}
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

(defn- plan-item-sessions-cell
  "Either a 'View session' button (lazy-fetches the session on first click)
   or an em-dash when there's no session yet.

   While an apply is in progress (`applying?`), the button is suppressed
   even for items that already have a `:sid` — the agent may still be
   streaming output, so we don't let the user navigate into a partially
   filled session view. The em-dash is shown instead to keep the cell
   from collapsing to empty space."
  [{:keys [item session-set applying? on-view-sessions]}]
  (if (and (:sid item) (not applying?))
    (let [loaded? (contains? session-set (:sid item))]
      [:> Button {:size "1" :variant "ghost" :color "indigo"
                  :on-click (fn []
                              (when-not loaded?
                                (rf/dispatch [:provisioning/fetch-plan-session
                                              (:sid item)]))
                              (on-view-sessions
                               {:resource-id   (:resource-id item)
                                :resource-name (:resource-name item)}))}
       [:> ScrollText {:size 12}] " View session"])
    [:> Text {:size "1" :color "gray"} "\u2014"]))

(defn- plan-item-row
  [{:keys [item session-set applying? on-view-sessions]}]
  ;; Managed rows render their scopes as a wrap-friendly row of gray
  ;; badges. External rows have no scopes (the agent forbids them — see
  ;; planExternal) and instead inherit from `source_role`, so they get a
  ;; single "inherits <source_role>" badge styled the same way; this keeps
  ;; the row visually consistent across types and signals at a glance
  ;; that the privileges come from a parent role rather than an explicit
  ;; grant list.
  [:> Table.Row
   [:> Table.Cell
    [:> Flex {:direction "column" :gap "1"}
     [:> Text {:size "2" :weight "medium"} (:resource-name item)]
     [:> Flex {:align "center" :gap "2" :wrap "wrap"}
      [:> Text {:size "1" :color "gray"
                :style {:font-family "var(--font-mono)" :font-size 11}}
       (or (:role-name item) (:role-input item))]
      [:> Text {:size "1" :color "gray"} "\u00b7"]
      (if (= (:type item) "external")
        [:> Badge {:color "gray" :variant "soft" :size "1"
                   :style {:font-family "var(--font-mono)" :font-size 11}}
         (str "inherits " (or (:source-role item) "\u2014"))]
        (for [scope (:scopes item)]
          ^{:key scope}
          [:> Badge {:color "gray" :variant "soft" :size "1"
                     :style {:font-family "var(--font-mono)" :font-size 11}}
           scope]))]]]
   [:> Table.Cell
    [plan-item-sessions-cell {:item             item
                              :session-set      session-set
                              :applying?        applying?
                              :on-view-sessions on-view-sessions}]]
   [:> Table.Cell
    [status-cell item]]])

(defn- derive-view-state
  "Pure function: takes a plan job + a list of all loaded sessions, returns
   every value the screen needs to render. Independently unit-testable."
  [plan-job sessions]
  (let [items     (or (:items plan-job) [])
        planning? (or (:planning? plan-job)
                      (some #(contains? #{"pending" "processing"} (:status %)) items))
        applying? (or (:applying? plan-job)
                      (some #(= "applying" (:status %)) items))
        busy?     (or planning? applying?)

        ;; Status vocabulary matches what the agent emits — see
        ;; `webapp.provisioning.data/plan-item-status`.
        ;; "out-of-sync" = plan ran, apply has work to do (= "ready to apply").
        ;; "in-sync"     = plan ran, role already matches desired state (no-op
        ;;                 at plan-time; no apply needed).
        ;; "skipped"     = apply ran but the agent re-checked state and found
        ;;                 it already matched; no psql was executed. Treated
        ;;                 the same as "success" for progress / done-ness.
        plan-done       (data/count-by-status items "out-of-sync")
        in-sync-count   (data/count-by-status items "in-sync")
        skipped-count   (data/count-by-status items "skipped")
        failed-count    (data/count-by-status items "failed")
        applied-count   (data/count-by-status items "success")
        cancelled-count (data/count-by-status items "Cancelled")
        total           (count items)

        terminal?    #(contains? #{"out-of-sync" "in-sync" "failed"
                                   "success" "skipped" "Cancelled"} (:status %))
        ;; in-sync items never enter apply, but they're effectively done as
        ;; far as the apply-progress bar is concerned. "skipped" is the
        ;; apply-time equivalent (agent re-checked and found nothing to do).
        apply-done?  #(contains? #{"success" "skipped" "failed"
                                   "Cancelled" "in-sync"} (:status %))
        all-planned? (and (pos? total) (not planning?) (every? terminal? items))
        all-done?    (and all-planned? (not applying?) (zero? plan-done))

        progress (cond
                   (zero? total) 0
                   applying?     (js/Math.round
                                  (* (/ (count (filter apply-done? items)) total) 100))
                   :else         (js/Math.round
                                  (* (/ (count (filter terminal? items)) total) 100)))]
    {:items            items
     :planning?        planning?
     :applying?        applying?
     :busy?            busy?
     :cancelled?       (:cancelled? plan-job)
     :apply-cancelled? (:apply-cancelled? plan-job)
     :plan-done        plan-done
     :in-sync-count    in-sync-count
     :skipped-count    skipped-count
     :failed-count     failed-count
     :applied-count    applied-count
     :cancelled-count  cancelled-count
     :total            total
     :all-planned?     all-planned?
     :all-done?        all-done?
     :progress         progress
     ;; Count of sessions available for the "N sessions" header button.
     ;; Sourced from plan-items (every item with a :sid corresponds to a
     ;; server-side session) rather than the loaded sessions list, so the
     ;; button surfaces all sessions even before any have been opened.
     :job-sessions-count (count (filter :sid items))
     :session-set      (set (map :id (filter :loaded? sessions)))}))

(defn- progress-color [{:keys [busy? failed-count]}]
  (cond busy?               "indigo"
        (pos? failed-count) "amber"
        :else               "green"))

(defn- subtitle-text
  [{:keys [total applied-count plan-done in-sync-count skipped-count
           failed-count cancelled-count]}]
  (str "Role provisioning \u2014 " total " roles \u00b7 "
       applied-count " applied \u00b7 "
       plan-done " ready \u00b7 "
       failed-count " failed"
       (when (pos? in-sync-count)   (str " \u00b7 " in-sync-count " in sync"))
       (when (pos? skipped-count)   (str " \u00b7 " skipped-count " skipped"))
       (when (pos? cancelled-count) (str " \u00b7 " cancelled-count " cancelled"))))

(defn job-detail-screen
  [_props]
  (fn [{:keys [on-back on-done on-run-in-background on-view-sessions]}]
    (let [plan-job @(rf/subscribe [:provisioning/plan-job])
          sessions @(rf/subscribe [:provisioning/sessions])
          {:keys [items progress session-set applying?] :as state}
          (derive-view-state plan-job sessions)]

      [:> Flex {:direction "column" :style {:flex 1 :min-height 0}}
       [:> Flex {:align "center" :gap "2" :mb "1"}
        [shared/back-button {:on-click on-back :label "Back to catalog"}]]

       [:> Flex {:align "center" :justify "between" :mb "4"}
        [:> Flex {:direction "column" :gap "1"}
         [:> Flex {:align "center" :gap "3"}
          [:> Heading {:size "7"} "Provision"]
          [status-badge state]]
         [:> Text {:size "2" :color "gray"} (subtitle-text state)]]
        [:> Flex {:align "center" :gap "2"}
         (for [{:keys [visible? label] :as a}
               (header-actions (assoc state
                                      :on-view-sessions     on-view-sessions
                                      :on-run-in-background on-run-in-background))
               :when visible?]
           ^{:key label}
           [action-button (dissoc a :visible?)])]]

       [:> Box {:mb "4"}
        [:> Progress {:value progress :color (progress-color state)}]]

       (for [{:keys [visible? color icon text]} (status-callouts state)
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
          (for [item items]
            ^{:key (:key item)}
            [plan-item-row {:item             item
                            :session-set      session-set
                            :applying?        applying?
                            :on-view-sessions on-view-sessions}])]]]

       [:> Flex {:align "center" :justify "between" :pt "4" :mt "4"
                 :style {:border-top "1px solid var(--gray-4)" :flex-shrink 0}}
        [shared/back-button {:on-click on-back :label "Back to catalog"}]
        [:> Flex {:gap "2"}
         (for [{:keys [visible? label] :as a}
               (footer-actions (assoc state :on-back on-back :on-done on-done))
               :when visible?]
           ^{:key label}
           [action-button (dissoc a :visible?)])]]])))
