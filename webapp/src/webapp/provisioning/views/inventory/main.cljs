(ns webapp.provisioning.views.inventory.main
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Checkbox Flex Heading
                                Table Tabs Text TextField Tooltip]]
   ["lucide-react" :refer [AlertCircle Check ChevronLeft ChevronRight Database
                            Key Loader2 Pencil Plus Rocket Search Upload
                            UserCog X]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.provisioning.data :as data]
   [webapp.provisioning.views.shared :as shared]))

;; ── Shared visual primitives ───────────────────────────────────────────────────

(defn- status-dot
  "Small color-coded circle used for status indicators."
  [{:keys [color size] :or {size 7}}]
  [:> Box {:style {:width         size
                   :height        size
                   :border-radius "50%"
                   :background    (str "var(--" color "-9)")
                   :flex-shrink   0}}])

;; ── Progress bar ───────────────────────────────────────────────────────────────

(defn progress-bar [resource]
  (let [completed (count (filter #(= "done" (data/get-segment-state (:key %) resource))
                                 data/segments))]
    [:> Flex {:direction "column" :gap "1" :style {:min-width 200}}
     [:> Flex {:align "center" :gap "2"}
      [:> Flex {:gap "1" :style {:flex 1}}
       (for [seg data/segments]
         (let [state    (data/get-segment-state (:key seg) resource)
               {:keys [bg text]} (get data/segment-states state)]
           ^{:key (:key seg)}
           [:> Tooltip {:content (str (:label seg) " — " text)}
            [:> Box {:style {:flex          "1 1 0"
                             :height        7
                             :border-radius 3
                             :background    bg
                             :cursor        "default"}}]]))]
      [:> Text {:size "1" :color "gray"
                :style {:white-space "nowrap" :min-width 28 :text-align "right"}}
       (str completed " / " (count data/segments))]]]))

;; ── Funnel cards ───────────────────────────────────────────────────────────────

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

;; ── Stage banner ───────────────────────────────────────────────────────────────

(defn stage-banner [{:keys [tab total-in-stage selected-in-stage on-action on-import-csv]}]
  (when (and (not= tab :inventory) (pos? total-in-stage))
    (let [count-val (if (pos? selected-in-stage) selected-in-stage total-in-stage)
          label-str (if (pos? selected-in-stage)
                      (str count-val " selected")
                      (str "all " count-val))
          manage?   (= tab :manage)
          color     (if manage? "amber" "blue")]
      [shared/callout-bar
       {:color    color
        :icon     (if manage? [:> UserCog {:size 18}] [:> Key {:size 18}])
        :title    (if manage? "Admin accounts needed" "Role provisioning needed")
        :subtitle (str (data/pluralize total-in-stage "resource")
                       (if manage?
                         " need an admin account before Hoop can provision roles."
                         " have an admin account but haven't had roles provisioned."))
        :actions  [:<>
                   [:> Button {:size "2" :variant "outline"
                               :color (if manage? "amber" "gray")
                               :on-click on-import-csv}
                    [:> Upload {:size 14}] " Import CSV"]
                   [:> Button {:size "2" :color (if manage? "amber" "indigo")
                               :on-click on-action}
                    (if manage? [:> UserCog {:size 14}] [:> Key {:size 14}])
                    (str " " (if manage? "Set up " "Provision ") label-str " →")]]}])))

;; ── Job status bar ─────────────────────────────────────────────────────────────

(defn job-status-bar [{:keys [job on-view on-dismiss]}]
  (let [done     (data/count-by-status (:items job) "done")
        failed   (data/count-by-status (:items job) "failed")
        total    (count (:items job))
        running? (< (+ done failed) total)
        color    (cond running? "indigo" (pos? failed) "amber" :else "green")
        icon     (cond
                   running?      [:span {:class "animate-spin inline-flex"
                                         :style {:color (str "var(--" color "-9)")}}
                                  [:> Loader2 {:size 14}]]
                   (pos? failed) [:> AlertCircle {:size 14}]
                   :else         [:> Check {:size 14}])
        title    (str (if (= (:type job) :admin-setup) "Admin setup" "Role provisioning")
                      " — " (second (re-find #"— (.+)" (:label job))))
        progress (if running?
                   (str (+ done failed) " / " total " processed")
                   (str done " succeeded" (when (pos? failed) (str ", " failed " failed"))))]
    [shared/callout-bar
     {:color   color
      :px      "4" :py "3"
      :icon    icon
      :extra   [:> Flex {:align "center" :gap "3"}
                [:> Text {:size "2" :weight "medium"} title]
                [:> Text {:size "2" :color "gray"} progress]]
      :actions [:<>
                [:> Button {:size "1" :variant "ghost" :on-click on-view}
                 (if running? "View progress" "View results")]
                (when-not running?
                  [:> Button {:size "1" :variant "ghost" :color "gray" :on-click on-dismiss}
                   [:> X {:size 12}]])]}]))

(defn plan-job-banner
  "Shows the active plan-job status when the user navigates back to the inventory
   while planning or applying is still running, or when results are ready."
  [{:keys [on-view]}]
  (let [plan-job @(rf/subscribe [:provisioning/plan-job])
        items    (or (:items plan-job) [])]
    (when (seq items)
      (let [applied   (data/count-by-status items "Applied")
            failed    (data/count-by-status items #{"Failed" "ApplyFailed"})
            ready     (data/count-by-status items #{"Create" "Update"})
            cancelled (data/count-by-status items "Cancelled")
            planning  (some #(contains? #{"pending" "processing"} (:status %)) items)
            applying  (some #(= "applying" (:status %)) items)
            busy?     (or planning applying)
            total     (count items)
            all-done  (and (not busy?) (zero? ready))
            all-success? (and all-done (zero? failed) (zero? cancelled))
            color     (cond busy?          "indigo"
                            (pos? failed)  "amber"
                            all-done       "green"
                            (pos? ready)   "blue"
                            :else          "gray")
            icon      (cond
                        busy?        [:span {:class "animate-spin inline-flex"
                                             :style {:color (str "var(--" color "-9)")}}
                                      [:> Loader2 {:size 14}]]
                        all-done     [:> Check {:size 14}]
                        (pos? ready) [:> Rocket {:size 14}]
                        :else        [:> AlertCircle {:size 14}])
            suffix    (str (when (pos? failed) (str ", " failed " failed"))
                           (when (pos? cancelled) (str ", " cancelled " cancelled")))
            progress  (cond
                        planning (str "Planning — " (- total (data/count-by-status items #{"pending" "processing"})) "/" total)
                        applying (str "Applying — " applied "/" total)
                        all-done (str applied " applied" suffix)
                        :else    (str ready " ready to apply" suffix))]
        (when-not all-success?
          [shared/callout-bar
           {:color   color
            :px      "4" :py "3"
            :icon    icon
            :extra   [:> Flex {:align "center" :gap "3"}
                      [:> Text {:size "2" :weight "medium"} "Role provisioning"]
                      [:> Text {:size "2" :color "gray"} progress]]
            :actions [:> Button {:size "1" :variant "ghost" :on-click on-view}
                      (if busy? "View progress" "View results")]}])))))

;; ── Floating action bar ────────────────────────────────────────────────────────

(defn floating-action-bar [{:keys [count-val admin-count roles-count ready-count
                                    on-add-admin on-configure-roles
                                    on-edit-admin on-edit-provision on-clear]}]
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
   (when (pos? ready-count)
     [:<>
      [:> Button {:size "2" :variant "soft" :color "gray" :on-click on-edit-admin}
       [:> Pencil {:size 14}] (str " Edit admin (" ready-count ")")]
      [:> Button {:size "2" :variant "soft" :color "gray" :on-click on-edit-provision}
       [:> Pencil {:size 14}] (str " Edit provision (" ready-count ")")]])
   (when (and (zero? admin-count) (zero? roles-count) (zero? ready-count))
     [:> Text {:size "2" :color "gray"} "No actions available."])
   [:> Button {:size "2" :variant "ghost" :color "gray" :on-click on-clear}
    [:> X {:size 14}]]])

;; ── Layout sections ────────────────────────────────────────────────────────────

(defn- inventory-header [{:keys [total-resources ready-count on-open-bulk-import]}]
  [:> Flex {:align "center" :justify "between" :mb "6"}
   [:> Flex {:direction "column" :gap "2"}
    [:> Heading {:size "8"} "Resource Catalog"]
    [:> Flex {:align "center" :gap "3"}
     [:> Text {:size "2" :color "gray"}
      "Track and provision every database resource connected to Hoop."]
     [:> Box {:style {:width 1 :height 12 :background "var(--gray-5)" :flex-shrink 0}}]
     [:> Text {:size "2" :color "gray"} (data/pluralize total-resources "resource")]
     [status-dot {:color "green" :size 5}]
     [:> Text {:size "2" :color "green"} (str ready-count " complete")]]]
   [:> Flex {:gap "2"}
    [:> Button {:size "3" :on-click on-open-bulk-import}
     [:> Plus {:size 16}] " Add to Inventory"]]])

(defn- inventory-tabs [{:keys [active-tab counts on-change]}]
  [:> Tabs.Root {:value         (name active-tab)
                 :onValueChange #(on-change (keyword %))}
   [:> Tabs.List {:mb "4"}
    [:> Tabs.Trigger {:value "inventory"}
     (str "Inventory (" (:inventory counts) ")")]
    [:> Tabs.Trigger {:value "manage"}
     (str "Manage (" (:manage counts) ")")]
    [:> Tabs.Trigger {:value "provision"}
     (str "Provision (" (:provision counts) ")")]]])

(defn- inventory-search [{:keys [search set-search active-tab]}]
  [:> Box {:mb "4"}
   [:> TextField.Root {:placeholder (str "Search "
                                         (cs/lower-case
                                          (get data/stage-label active-tab "inventory"))
                                         " resources…")
                       :value     search
                       :onChange  #(set-search (.. % -target -value))
                       :style     {:max-width 360}}
    [:> TextField.Slot [:> Search {:size 14}]]]])

(defn- inventory-empty-state [{:keys [search active-tab on-clear-search]}]
  [:> Table.Row
   [:> Table.Cell {:col-span 7}
    [:> Flex {:direction "column" :align "center" :justify "center" :py "9" :gap "3"}
     [:> Box {:style {:color "var(--gray-5)" :display "flex"}}
      (if (seq search)
        [:> Search {:size 30 :stroke-width 1.5}]
        [:> Database {:size 30 :stroke-width 1.5}])]
     [:> Text {:size "2" :weight "medium" :color "gray"}
      (cond
        (seq search)              (str "No results for \"" search "\"")
        (= active-tab :manage)    "All resources have admin accounts configured"
        (= active-tab :provision) "No resources are ready for provisioning yet"
        :else                     "No resources found")]
     (when (seq search)
       [:> Button {:variant "ghost" :size "1" :color "gray"
                   :on-click on-clear-search}
        "Clear search"])]]])

(defn- row-action-button
  "Renders the per-row stage action button using the central stage-action map."
  [{:keys [resource on-open-bulk-admin on-open-bulk-roles]}]
  (let [stage   (:stage resource)
        action  (get data/stage-action stage)
        handler (case (:handler-key action)
                  :on-open-bulk-admin #(on-open-bulk-admin [resource])
                  :on-open-bulk-roles #(on-open-bulk-roles [resource])
                  nil)]
    (when action
      [:> Button (cond-> {:variant (:variant action) :size "1"}
                   (:color action) (assoc :color (:color action))
                   handler         (assoc :on-click handler))
       (:row-label action)])))

(defn- inventory-row
  [{:keys [resource selected? hovered? on-toggle on-hover-on on-hover-off
           on-open-bulk-admin on-open-bulk-roles]}]
  [:> Table.Row
   {:style          {:background (data/row-bg (:stage resource) selected? hovered?)
                     :cursor     "pointer"}
    :on-click       on-toggle
    :on-mouse-enter on-hover-on
    :on-mouse-leave on-hover-off}
   [:> Table.Cell {:on-click #(.stopPropagation %)}
    [:> Checkbox {:checked selected? :onCheckedChange on-toggle}]]
   [:> Table.Cell [:> Text {:size "2" :weight "medium"} (:name resource)]]
   [:> Table.Cell [:> Badge {:color "gray" :variant "soft" :size "1"} (:db-type resource)]]
   [:> Table.Cell
    [:> Text {:size "2" :style {:font-family "var(--font-mono)" :font-size 12}}
     (:address resource)]]
   [:> Table.Cell
    (if (:admin resource)
      [:> Flex {:align "center" :gap "2"}
       [status-dot {:color "green"}]
       [:> Text {:size "2"} (:admin resource)]]
      [:> Flex {:align "center" :gap "2"}
       [status-dot {:color "amber"}]
       [:> Text {:size "2" :color "gray"} "Not configured"]])]
   [:> Table.Cell {:style {:min-width 220}} [progress-bar resource]]
   [:> Table.Cell {:on-click #(.stopPropagation %)}
    [:> Flex {:align "center" :gap "1"}
     [row-action-button {:resource           resource
                         :on-open-bulk-admin on-open-bulk-admin
                         :on-open-bulk-roles on-open-bulk-roles}]]]])

(defn- inventory-table
  [{:keys [page-rows visible all-visible-selected some-visible-selected
           selected-ids hovered-row search active-tab
           on-toggle-select on-toggle-all on-set-hovered on-set-search
           on-open-bulk-admin on-open-bulk-roles]}]
  [:> Table.Root {:variant "surface"}
   [:> Table.Header
    [:> Table.Row
     [:> Table.ColumnHeaderCell {:style {:width 48}}
      [:> Checkbox {:checked         (cond
                                       (and all-visible-selected (pos? (count visible))) true
                                       some-visible-selected                             "indeterminate"
                                       :else                                             false)
                    :onCheckedChange on-toggle-all}]]
     [:> Table.ColumnHeaderCell "Name"]
     [:> Table.ColumnHeaderCell "Type"]
     [:> Table.ColumnHeaderCell "Host"]
     [:> Table.ColumnHeaderCell "Admin account"]
     [:> Table.ColumnHeaderCell {:style {:min-width 220}} "Setup progress"]
     [:> Table.ColumnHeaderCell]]]
   [:> Table.Body
    (if (empty? visible)
      [inventory-empty-state {:search          search
                              :active-tab      active-tab
                              :on-clear-search #(on-set-search "")}]
      (doall
       (for [r page-rows]
         ^{:key (:id r)}
         [inventory-row
          {:resource           r
           :selected?          (contains? selected-ids (:id r))
           :hovered?           (= hovered-row (:id r))
           :on-toggle          #(on-toggle-select (:id r))
           :on-hover-on        #(on-set-hovered (:id r))
           :on-hover-off       #(on-set-hovered nil)
           :on-open-bulk-admin on-open-bulk-admin
           :on-open-bulk-roles on-open-bulk-roles}])))]])

(defn- pagination
  [{:keys [total-visible selected-count safe-page total-pages on-change]}]
  [:> Flex {:align "center" :justify "between" :mt "3"}
   [:> Text {:size "1" :color "gray"}
    (str (data/pluralize total-visible "resource")
         (when (pos? selected-count)
           (str " \u00b7 " selected-count " selected")))]
   (when (> total-pages 1)
     [:> Flex {:align "center" :gap "2"}
      [:> Button {:size "1" :variant "ghost" :color "gray"
                  :disabled (zero? safe-page)
                  :on-click #(on-change (dec safe-page))}
       [:> ChevronLeft {:size 14}]]
      [:> Text {:size "1" :color "gray"}
       (str (inc safe-page) " / " total-pages)]
      [:> Button {:size "1" :variant "ghost" :color "gray"
                  :disabled (>= (inc safe-page) total-pages)
                  :on-click #(on-change (inc safe-page))}
       [:> ChevronRight {:size 14}]]])])

;; initial page size is 50
(def ^:private hub-page-size 50)

(defn- resolve-stage-handler
  "Looks up the bulk handler fn for the given stage from the props passed
   to `view`. Returns nil if no action is configured for that stage."
  [stage props]
  (when-let [k (:handler-key (get data/stage-action stage))]
    (get props k)))

(defn view
  [{:keys [resources selected-ids set-selected-ids
           search set-search active-tab set-active-tab
           page set-page
           jobs dismissed-job-ids set-dismissed-job-ids
           hovered-row set-hovered-row
           on-set-screen on-open-bulk-admin on-open-bulk-roles on-open-bulk-import]
    :as   props}]
  (let [stage-filter   (get data/tab->stage active-tab)
        stage-filtered (if stage-filter
                         (filterv #(= stage-filter (:stage %)) resources)
                         resources)

        progress-score {:needs-admin 0 :needs-roles 1 :ready 2}
        visible        (->> stage-filtered
                             (filterv (fn [r]
                                        (or (empty? search)
                                            (cs/includes?
                                             (cs/lower-case (:name r))
                                             (cs/lower-case search)))))
                             (sort-by #(get progress-score (:stage %) 0))
                             vec)
        total-visible  (count visible)
        total-pages    (js/Math.ceil (/ total-visible hub-page-size))
        safe-page      (min page (max 0 (dec total-pages)))
        start-idx      (* safe-page hub-page-size)
        end-idx        (min total-visible (+ start-idx hub-page-size))
        page-rows      (subvec (vec visible) start-idx end-idx)

        counts {:inventory (count resources)
                :manage    (count (filter #(= :needs-admin (:stage %)) resources))
                :provision (count (filter #(= :needs-roles (:stage %)) resources))}

        selected-resources     (filter #(selected-ids (:id %)) resources)
        selected-needing-admin (filter #(= :needs-admin (:stage %)) selected-resources)
        selected-needing-roles (filter #(= :needs-roles (:stage %)) selected-resources)
        selected-ready         (filter #(= :ready (:stage %)) selected-resources)
        selected-in-stage      (count (filter #(= stage-filter (:stage %)) selected-resources))

        all-visible-selected   (and (pos? (count visible))
                                    (every? #(selected-ids (:id %)) visible))
        some-visible-selected  (some #(selected-ids (:id %)) visible)

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
                        (set-active-tab tab)
                        (set-selected-ids #{})
                        (set-search ""))

        latest-active-job (last (filterv #(not (contains? dismissed-job-ids (:id %))) jobs))
        ready-count       (count (filter #(= :ready (:stage %)) resources))

        stage-handler     (resolve-stage-handler stage-filter props)
        run-stage-action  (fn []
                            (when stage-handler
                              (let [targets (if (pos? selected-in-stage)
                                              (filterv #(and (selected-ids (:id %))
                                                             (= stage-filter (:stage %)))
                                                       resources)
                                              stage-filtered)]
                                (stage-handler targets))))
        run-import-csv    (fn []
                            (when stage-handler
                              (stage-handler stage-filtered "csv")))]

    [:> Box {:class "flex-1 overflow-y-auto"}
     [inventory-header {:total-resources     (count resources)
                        :ready-count         ready-count
                        :on-open-bulk-import on-open-bulk-import}]

     [funnel-cards resources]

     (when latest-active-job
       [job-status-bar {:job        latest-active-job
                        :on-view    #(on-set-screen :job-detail (:id latest-active-job))
                        :on-dismiss #(set-dismissed-job-ids
                                      (fn [s] (conj s (:id latest-active-job))))}])

     [plan-job-banner {:on-view #(on-set-screen :job-detail)}]

     [inventory-tabs {:active-tab active-tab
                      :counts     counts
                      :on-change  change-tab}]

     [stage-banner {:tab               active-tab
                    :total-in-stage    (count stage-filtered)
                    :selected-in-stage selected-in-stage
                    :on-action         run-stage-action
                    :on-import-csv     run-import-csv}]

     [inventory-search {:search     search
                        :set-search set-search
                        :active-tab active-tab}]

     [inventory-table {:page-rows             page-rows
                       :visible               visible
                       :all-visible-selected  all-visible-selected
                       :some-visible-selected some-visible-selected
                       :selected-ids          selected-ids
                       :hovered-row           hovered-row
                       :search                search
                       :active-tab            active-tab
                       :on-toggle-select      toggle-select
                       :on-toggle-all         toggle-all
                       :on-set-hovered        set-hovered-row
                       :on-set-search         set-search
                       :on-open-bulk-admin    on-open-bulk-admin
                       :on-open-bulk-roles    on-open-bulk-roles}]

     [pagination {:total-visible  total-visible
                  :selected-count (count selected-ids)
                  :safe-page      safe-page
                  :total-pages    total-pages
                  :on-change      set-page}]

     (when (pos? (count selected-ids))
       [floating-action-bar
        {:count-val          (count selected-ids)
         :admin-count        (count selected-needing-admin)
         :roles-count        (count selected-needing-roles)
         :ready-count        (count selected-ready)
         :on-add-admin       #(on-open-bulk-admin (vec selected-needing-admin))
         :on-configure-roles #(on-open-bulk-roles (vec selected-needing-roles))
         :on-edit-admin      #(on-open-bulk-admin (vec selected-ready))
         :on-edit-provision  #(on-open-bulk-roles (vec selected-ready))
         :on-clear           #(set-selected-ids #{})}])]))
