(ns webapp.provisioning.views.inventory.main
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Checkbox Flex Heading
                                Table Tabs Text TextField Tooltip]]
   ["lucide-react" :refer [AlertCircle Check ChevronLeft ChevronRight Database
                            Key Loader2 Plus Search Upload
                            UserCog X]]
   [clojure.string :as cs]
   [webapp.provisioning.data :as data]))

;; ── Progress bar ───────────────────────────────────────────────────────────
(defn progress-bar [resource]
  (let [completed (count (filter #(= "done" (data/get-segment-state (:key %) resource))
                                 data/segments))]
    [:> Flex {:direction "column" :gap "1" :style {:min-width 200}}
     [:> Flex {:align "center" :gap "2"}
      [:> Flex {:gap "1" :style {:flex 1}}
       (for [seg data/segments]
         (let [state (data/get-segment-state (:key seg) resource)
               bg    (case state
                       "done"   "var(--green-9)"
                       "active" "var(--indigo-9)"
                       "var(--gray-4)")]
           ^{:key (:key seg)}
           [:> Tooltip {:content (str (:label seg) " — "
                                      (case state
                                        "done"   "complete"
                                        "active" "action required"
                                        "complete previous steps first"))}
            [:> Box {:style {:flex      "1 1 0"
                             :height    7
                             :border-radius 3
                             :background bg
                             :cursor    "default"}}]]))]
      [:> Text {:size "1" :color "gray"
                :style {:white-space "nowrap" :min-width 28 :text-align "right"}}
       (str completed " / " (count data/segments))]]]))

;; ── Funnel cards ───────────────────────────────────────────────────────────
(defn funnel-cards [resources]
  (let [total       (count resources)
        needs-admin (count (filter #(= :needs-admin (:stage %)) resources))
        needs-roles (count (filter #(= :needs-roles (:stage %)) resources))
        stages [{:label "Inventory" :big-num total         :detail (str total " total")
                 :fill 1                                   :fill-color "var(--gray-7)"
                 :pending 0   :pending-color "gray"}
                {:label "Manage"    :big-num needs-admin   :detail (str needs-admin " pending")
                 :fill (if (pos? total) (/ needs-admin total) 0)
                 :fill-color "var(--amber-9)"
                 :pending needs-admin :pending-color "amber"}
                {:label "Provision" :big-num needs-roles   :detail (str needs-roles " pending")
                 :fill (if (pos? total) (/ needs-roles total) 0)
                 :fill-color "var(--blue-9)"
                 :pending needs-roles :pending-color "blue"}]]
    [:> Flex {:align "stretch" :mb "5" :gap "0"}
     (doall
      (for [[i s] (map-indexed vector stages)]
        ^{:key (:label s)}
        [:<>
         (when (pos? i)
           [:> Flex {:align "center" :px "1" :style {:color "var(--gray-4)" :flex-shrink 0}}
            [:> ChevronRight {:size 16}]])
         [:> Box {:style {:flex 1
                          :background "var(--color-panel-solid)"
                          :border-left   "1px solid var(--gray-4)"
                          :border-right  "1px solid var(--gray-4)"
                          :border-bottom "1px solid var(--gray-4)"
                          :border-top    (str "3px solid " (nth data/funnel-accent i))
                          :border-radius "var(--radius-3)"
                          :padding       "14px 16px 12px"}}
          [:> Flex {:direction "column" :gap "3"}
           [:> Flex {:align "center" :justify "between"}
            [:> Text {:size "1" :weight "medium"
                      :style {:letter-spacing "0.04em" :text-transform "uppercase"
                              :font-size 10 :color "var(--gray-9)"}}
             (:label s)]
            [:> Flex {:align "center" :gap "2"}
             (when (pos? (:pending s))
               [:> Badge {:color (:pending-color s) :variant "soft" :size "1"}
                (str (:pending s) " pending")])
             [:> Text {:size "1" :style {:font-family "var(--font-mono)" :font-size 10
                                         :color "var(--gray-6)"}}
              (nth data/funnel-step-id i)]]]
           [:> Flex {:align "baseline" :gap "2"}
            [:> Text {:size "7" :weight "bold" :style {:line-height 1 :color "var(--gray-12)"}}
             (:big-num s)]
            [:> Text {:size "1" :color "gray"} (:detail s)]]
           [:> Box {:style {:height 5 :background "var(--gray-3)" :border-radius 99 :overflow "hidden"}}
            [:> Box {:style {:height     "100%"
                             :width      (str (* (:fill s) 100) "%")
                             :background (:fill-color s)
                             :border-radius 99
                             :transition "width 0.6s ease"}}]]]]]))]))

;; ── Stage banner ───────────────────────────────────────────────────────────
(defn stage-banner [{:keys [tab total-in-stage selected-in-stage on-action on-import-csv]}]
  (when (and (not= tab :inventory) (pos? total-in-stage))
    (let [count-val (if (pos? selected-in-stage) selected-in-stage total-in-stage)
          label-str (if (pos? selected-in-stage)
                      (str count-val " selected")
                      (str "all " count-val))
          manage? (= tab :manage)]
      [:> Flex {:align "center" :justify "between" :gap "4" :px "5" :py "4" :mb "4"
                :style {:background     (if manage? "var(--amber-2)" "var(--blue-2)")
                        :border-top     (str "1px solid " (if manage? "var(--amber-5)" "var(--blue-5)"))
                        :border-right   (str "1px solid " (if manage? "var(--amber-5)" "var(--blue-5)"))
                        :border-bottom  (str "1px solid " (if manage? "var(--amber-5)" "var(--blue-5)"))
                        :border-left    (str "4px solid " (if manage? "var(--amber-9)" "var(--blue-9)"))
                        :border-radius  "var(--radius-3)"}}
       [:> Flex {:align "center" :gap "3"}
        [:> Box {:style {:color   (if manage? "var(--amber-9)" "var(--blue-9)")
                         :display "flex" :flex-shrink 0}}
         (if manage? [:> UserCog {:size 18}] [:> Key {:size 18}])]
        [:> Flex {:direction "column" :gap "0"}
         [:> Text {:size "2" :weight "medium"}
          (if manage? "Admin accounts needed" "Role provisioning needed")]
         [:> Text {:size "1" :color "gray"}
          (str total-in-stage " resource"
               (when (not= total-in-stage 1) "s")
               (if manage?
                 " need an admin account before Hoop can provision roles."
                 " have an admin account but haven't had roles provisioned."))]]]
       [:> Flex {:gap "2" :style {:flex-shrink 0}}
        [:> Button {:size "2" :variant "outline"
                    :color (if manage? "amber" "gray")
                    :on-click on-import-csv}
         [:> Upload {:size 14}] " Import CSV"]
        [:> Button {:size "2" :color (if manage? "amber" "indigo")
                    :on-click on-action}
         (if manage? [:> UserCog {:size 14}] [:> Key {:size 14}])
         (str " " (if manage? "Set up " "Provision ") label-str " →")]]])))

;; ── Job status bar ─────────────────────────────────────────────────────────
(defn job-status-bar [{:keys [job on-view on-dismiss]}]
  (let [done    (count (filter #(= "done" (:status %)) (:items job)))
        failed  (count (filter #(= "failed" (:status %)) (:items job)))
        total   (count (:items job))
        running? (< (+ done failed) total)
        color   (cond running? "indigo" (pos? failed) "amber" :else "green")]
    [:> Flex {:align "center" :justify "between" :px "4" :py "3" :mb "4"
              :style {:background   (str "var(--" color "-2)")
                      :border-top   (str "1px solid var(--" color "-5)")
                      :border-right (str "1px solid var(--" color "-5)")
                      :border-bottom (str "1px solid var(--" color "-5)")
                      :border-left  (str "4px solid var(--" color "-9)")
                      :border-radius "var(--radius-3)"}}
     [:> Flex {:align "center" :gap "3"}
      (cond
        running?     [:span {:class "animate-spin inline-flex"
                             :style {:color (str "var(--" color "-9)")}}
                      [:> Loader2 {:size 14}]]
        (pos? failed) [:> Box {:style {:color "var(--amber-9)" :display "flex"}}
                       [:> AlertCircle {:size 14}]]
        :else         [:> Box {:style {:color "var(--green-9)" :display "flex"}}
                       [:> Check {:size 14}]])
      [:> Text {:size "2" :weight "medium"}
       (str (if (= (:type job) :admin-setup) "Admin setup" "Role provisioning")
            " — " (second (re-find #"— (.+)" (:label job))))]
      [:> Text {:size "2" :color "gray"}
       (if running?
         (str (+ done failed) " / " total " processed")
         (str done " succeeded" (when (pos? failed) (str ", " failed " failed"))))]]
     [:> Flex {:align "center" :gap "2"}
      [:> Button {:size "1" :variant "ghost" :on-click on-view}
       (if running? "View progress" "View results")]
      (when-not running?
        [:> Button {:size "1" :variant "ghost" :color "gray" :on-click on-dismiss}
         [:> X {:size 12}]])]]))

(defn floating-action-bar [{:keys [count-val admin-count roles-count
                                    on-add-admin on-configure-roles on-clear]}]
  [:> Flex {:align "center" :gap "3" :px "5" :py "3"
            :style {:position    "fixed"
                    :bottom      80
                    :left        "50%"
                    :transform   "translateX(-50%)"
                    :z-index     40
                    :background  "var(--gray-1)"
                    :border      "1px solid var(--gray-6)"
                    :border-radius "var(--radius-4)"
                    :box-shadow  "0 8px 32px rgba(0,0,0,0.12)"
                    :white-space "nowrap"}}
   [:> Text {:size "2" :weight "medium"} (str count-val " selected")]
   [:> Box {:style {:width 1 :height 16 :background "var(--gray-6)"}}]
   (when (pos? admin-count)
     [:> Button {:size "2" :color "amber" :variant "soft" :on-click on-add-admin}
      [:> UserCog {:size 14}] (str " Manage (" admin-count ")")])
   (when (pos? roles-count)
     [:> Button {:size "2" :variant "soft" :on-click on-configure-roles}
      [:> Key {:size 14}] (str " Provision (" roles-count ")")])
   (when (and (zero? admin-count) (zero? roles-count))
     [:> Text {:size "2" :color "gray"} "No actions available."])
   [:> Button {:size "2" :variant "ghost" :color "gray" :on-click on-clear}
    [:> X {:size 14}]]])

;; initial page size is 50
(def ^:private hub-page-size 50)



(defn view
  [{:keys [resources selected-ids set-selected-ids
           search set-search active-tab set-active-tab
           page set-page
           jobs dismissed-job-ids set-dismissed-job-ids
           hovered-row set-hovered-row
           on-set-screen on-open-bulk-admin on-open-bulk-roles on-open-bulk-import]}]

  ;;; initially i am keeping fn inside the component unless I need to use this in another namespace
  (let [stage-filter   (get data/tab->stage active-tab)
        stage-filtered (if stage-filter
                         (filterv #(= stage-filter (:stage %)) resources)
                         resources)


        visible        (filterv (fn [r]
                                  (or (empty? search)
                                      (cs/includes?
                                       (cs/lower-case (:name r))
                                       (cs/lower-case search))))
                                stage-filtered)
        total-visible  (count visible)
        total-pages    (js/Math.ceil (/ total-visible hub-page-size))
        safe-page      (min page (max 0 (dec total-pages)))
        start-idx      (* safe-page hub-page-size)
        end-idx        (min total-visible (+ start-idx hub-page-size))
        page-rows      (subvec (vec visible) start-idx end-idx)

        counts {:inventory (count resources)
                :manage    (count (filter #(= :needs-admin (:stage %)) resources))
                :provision (count (filter #(= :needs-roles (:stage %)) resources))}

        selected-resources    (filter #(selected-ids (:id %)) resources)
        selected-needing-admin (filter #(= :needs-admin (:stage %)) selected-resources)
        selected-needing-roles (filter #(= :needs-roles (:stage %)) selected-resources)
        selected-in-stage     (count (filter #(= stage-filter (:stage %)) selected-resources))

        all-visible-selected  (and (pos? (count visible))
                                   (every? #(selected-ids (:id %)) visible))
        some-visible-selected (some #(selected-ids (:id %)) visible)

        toggle-select (fn [id]
                        (set-selected-ids
                         (fn [s] (if (s id) (disj s id) (conj s id)))))
        toggle-all    (fn []
                        (if all-visible-selected
                          (set-selected-ids
                           (fn [s] (reduce disj s (map :id visible))))
                          (set-selected-ids
                           (fn [s] (into s (map :id visible))))))
        change-tab    (fn [tab]
                        (set-active-tab (keyword tab))
                        (set-selected-ids #{})
                        (set-search ""))

        latest-active-job (last (filterv #(not (contains? dismissed-job-ids (:id %))) jobs))
        ready-count (count (filter #(= :ready (:stage %)) resources))]

    [:> Box {:class "flex-1 overflow-y-auto"}
     ;; Header
     [:> Flex {:align "center" :justify "between" :mb "6"}
      [:> Flex {:direction "column" :gap "2"}
       [:> Heading {:size "8"} "Resource Catalog"]
       [:> Flex {:align "center" :gap "3"}
        [:> Text {:size "2" :color "gray"}
         "Track and provision every database resource connected to Hoop."]
        [:> Box {:style {:width 1 :height 12 :background "var(--gray-5)" :flex-shrink 0}}]
        [:> Text {:size "2" :color "gray"} (str (count resources) " resources")]
        [:> Box {:style {:width 5 :height 5 :border-radius "50%"
                         :background "var(--green-9)" :flex-shrink 0}}]
        [:> Text {:size "2" :color "green"} (str ready-count " complete")]]]
      [:> Flex {:gap "2"}
       [:> Button {:size "3" :on-click on-open-bulk-import}
        [:> Plus {:size 16}] " Add to Inventory"]]]

     ;; Funnel
     [funnel-cards resources]

     ;; Active job banner
     (when latest-active-job
       [job-status-bar {:job        latest-active-job
                        :on-view    #(on-set-screen :job-detail (:id latest-active-job))
                        :on-dismiss #(set-dismissed-job-ids
                                      (fn [s] (conj s (:id latest-active-job))))}])

     ;; Tabs
     [:> Tabs.Root {:value (name active-tab)
                    :onValueChange #(change-tab %)}
      [:> Tabs.List {:mb "4"}
       [:> Tabs.Trigger {:value "inventory"}
        (str "Inventory (" (:inventory counts) ")")]
       [:> Tabs.Trigger {:value "manage"}
        (str "Manage (" (:manage counts) ")")]
       [:> Tabs.Trigger {:value "provision"}
        (str "Provision (" (:provision counts) ")")]]]

     ;; Stage banner
     [stage-banner {:tab             active-tab
                    :total-in-stage  (count stage-filtered)
                    :selected-in-stage selected-in-stage
                    :on-action       (fn []
                                       (let [targets (if (pos? selected-in-stage)
                                                       (filterv #(and (selected-ids (:id %))
                                                                      (= stage-filter (:stage %)))
                                                                resources)
                                                       stage-filtered)]
                                         (case active-tab
                                           :manage    (on-open-bulk-admin targets)
                                           :provision (on-open-bulk-roles targets)
                                           nil)))
                    :on-import-csv   (fn []
                                       (case active-tab
                                         :manage    (on-open-bulk-admin stage-filtered "csv")
                                         :provision (on-open-bulk-roles stage-filtered "csv")
                                         nil))}]

     ;; Search
     [:> Box {:mb "4"}
      [:> TextField.Root {:placeholder (str "Search "
                                            (cs/lower-case
                                             (get data/stage-label active-tab "inventory"))
                                            " resources…")
                          :value     search
                          :onChange  #(set-search (.. % -target -value))
                          :style     {:max-width 360}}
       [:> TextField.Slot [:> Search {:size 14}]]]]

     ;; Table
     [:> Table.Root {:variant "surface"}
      [:> Table.Header
       [:> Table.Row
        [:> Table.ColumnHeaderCell {:style {:width 48}}
         [:> Checkbox {:checked (cond
                                  (and all-visible-selected (pos? (count visible))) true
                                  some-visible-selected "indeterminate"
                                  :else false)
                       :onCheckedChange toggle-all}]]
        [:> Table.ColumnHeaderCell "Name"]
        [:> Table.ColumnHeaderCell "Type"]
        [:> Table.ColumnHeaderCell "Host"]
        [:> Table.ColumnHeaderCell "Admin account"]
        [:> Table.ColumnHeaderCell {:style {:min-width 220}} "Setup progress"]
        [:> Table.ColumnHeaderCell]]]
      [:> Table.Body
       (if (empty? visible)
         [:> Table.Row
          [:> Table.Cell {:col-span 7}
           [:> Flex {:direction "column" :align "center" :justify "center" :py "9" :gap "3"}
            [:> Box {:style {:color "var(--gray-5)" :display "flex"}}
             (if (seq search)
               [:> Search {:size 30 :stroke-width 1.5}]
               [:> Database {:size 30 :stroke-width 1.5}])]
            [:> Text {:size "2" :weight "medium" :color "gray"}
             (cond
               (seq search) (str "No results for \"" search "\"")
               (= active-tab :manage) "All resources have admin accounts configured"
               (= active-tab :provision) "No resources are ready for provisioning yet"
               :else "No resources found")]
            (when (seq search)
              [:> Button {:variant "ghost" :size "1" :color "gray"
                          :on-click #(set-search "")}
               "Clear search"])]]]

         (doall
          (for [r page-rows]
            ^{:key (:id r)}
            [:> Table.Row
             {:style    {:background (data/row-bg (:stage r)
                                                   (contains? selected-ids (:id r))
                                                   (= hovered-row (:id r)))
                         :cursor "pointer"}
              :on-click #(toggle-select (:id r))
              :on-mouse-enter #(set-hovered-row (:id r))
              :on-mouse-leave #(set-hovered-row nil)}
             [:> Table.Cell {:on-click #(.stopPropagation %)}
              [:> Checkbox {:checked   (contains? selected-ids (:id r))
                            :onCheckedChange #(toggle-select (:id r))}]]
             [:> Table.Cell [:> Text {:size "2" :weight "medium"} (:name r)]]
             [:> Table.Cell [:> Badge {:color "gray" :variant "soft" :size "1"} (:db-type r)]]
             [:> Table.Cell [:> Text {:size "2" :style {:font-family "var(--font-mono)" :font-size 12}}
                             (:address r)]]
             [:> Table.Cell
              (if (:admin r)
                [:> Flex {:align "center" :gap "2"}
                 [:> Box {:style {:width 7 :height 7 :border-radius "50%"
                                  :background "var(--green-9)" :flex-shrink 0}}]
                 [:> Text {:size "2"} (:admin r)]]
                [:> Flex {:align "center" :gap "2"}
                 [:> Box {:style {:width 7 :height 7 :border-radius "50%"
                                  :background "var(--amber-9)" :flex-shrink 0}}]
                 [:> Text {:size "2" :color "gray"} "Not configured"]])]
             [:> Table.Cell {:style {:min-width 220}} [progress-bar r]]
             [:> Table.Cell {:on-click #(.stopPropagation %)}
              [:> Flex {:align "center" :gap "1"}
               (case (:stage r)
                 :needs-admin [:> Button {:variant "ghost" :size "1"
                                          :on-click #(on-open-bulk-admin [r])}
                               "Set up admin"]
                 :needs-roles [:> Button {:variant "ghost" :size "1"
                                          :on-click #(on-open-bulk-roles [r])}
                               "Provision roles"]
                 :ready       [:> Button {:variant "ghost" :size "1" :color "gray"}
                               "Manage"]
                 nil)]]])))]]

     ;; Pagination + Footer
     [:> Flex {:align "center" :justify "between" :mt "3"}
      [:> Text {:size "1" :color "gray"}
       (str total-visible " resource" (when (not= 1 total-visible) "s")
            (when (pos? (count selected-ids))
              (str " \u00b7 " (count selected-ids) " selected")))]
      (when (> total-pages 1)
        [:> Flex {:align "center" :gap "2"}
         [:> Button {:size "1" :variant "ghost" :color "gray"
                     :disabled (zero? safe-page)
                     :on-click #(set-page (dec safe-page))}
          [:> ChevronLeft {:size 14}]]
         [:> Text {:size "1" :color "gray"}
          (str (inc safe-page) " / " total-pages)]
         [:> Button {:size "1" :variant "ghost" :color "gray"
                     :disabled (>= (inc safe-page) total-pages)
                     :on-click #(set-page (inc safe-page))}
          [:> ChevronRight {:size 14}]]])]

     ;; Floating action bar
     (when (pos? (count selected-ids))
       [floating-action-bar
        {:count-val          (count selected-ids)
         :admin-count        (count selected-needing-admin)
         :roles-count        (count selected-needing-roles)
         :on-add-admin       #(on-open-bulk-admin (vec selected-needing-admin))
         :on-configure-roles #(on-open-bulk-roles (vec selected-needing-roles))
         :on-clear           #(set-selected-ids #{})}])]))
