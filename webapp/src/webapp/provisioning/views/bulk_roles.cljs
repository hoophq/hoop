(ns webapp.provisioning.views.bulk-roles
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Callout Card Checkbox
                                Flex Heading Skeleton Text TextField]]
   ["lucide-react" :refer [ArrowLeft Check FileText Info Key Loader2
                            Sparkles Upload]]
   [reagent.core :as r]
   [webapp.provisioning.data :as data]))

;; ── Method card ────────────────────────────────────────────────────────────────
(defn method-card [{:keys [selected icon title description badge on-click]}]
  [:> Card {:style {:flex 1 :cursor "pointer" :position "relative"
                    :border-color (when selected "var(--indigo-9)")
                    :border-width (if selected 2 1)
                    :background   (when selected "var(--indigo-2)")}
            :on-click on-click}
   (when selected
     [:> Box {:style {:position "absolute" :top 10 :right 10
                      :width 18 :height 18 :border-radius "50%"
                      :background "var(--indigo-9)"
                      :display "flex" :align-items "center" :justify-content "center"
                      :color "white"}}
      [:> Check {:size 10}]])
   [:> Flex {:direction "column" :gap "2" :p "1"}
    [:> Box {:style {:width 32 :height 32 :border-radius "var(--radius-2)"
                     :display "flex" :align-items "center" :justify-content "center"
                     :background (if selected "var(--indigo-4)" "var(--gray-3)")
                     :color      (if selected "var(--indigo-11)" "var(--gray-9)")
                     :margin-bottom 2}}
     icon]
    [:> Flex {:direction "column" :gap "1"}
     [:> Flex {:align "center" :gap "1"}
      [:> Text {:size "2" :weight "medium"} title]
      (when badge [:> Badge {:color "indigo" :variant "soft" :size "1"} badge])]
     [:> Text {:size "1" :color "gray"} description]]]])

;; ── Role discovery table (bind mode) ──────────────────────────────────────────
(defn role-discovery-table [{:keys [resources discovered-roles selected-roles on-toggle]}]
  (let [all-roles (mapcat (fn [r]
                            (map (fn [role] {:resource r :role role})
                                 (get discovered-roles (:id r) [])))
                          resources)]
    [:> Box {:style {:flex 1 :overflow-y "auto"
                     :border "1px solid var(--gray-5)"
                     :border-radius "var(--radius-2)"}}
     ;; Header
     [:> Flex {:px "3" :py "2"
               :style {:background "var(--gray-3)"
                       :border-bottom "1px solid var(--gray-5)"
                       :position "sticky" :top 0}}
      [:> Box {:style {:width 36 :flex-shrink 0}}]
      [:> Box {:style {:flex "2 1 0"}}
       [:> Text {:size "1" :color "gray" :weight "medium"} "Resource"]]
      [:> Box {:style {:flex "2 1 0"}}
       [:> Text {:size "1" :color "gray" :weight "medium"} "Role"]]
      [:> Box {:style {:width 100 :flex-shrink 0}}
       [:> Text {:size "1" :color "gray" :weight "medium"} "Type"]]
      [:> Box {:style {:width 80 :flex-shrink 0}}
       [:> Text {:size "1" :color "gray" :weight "medium"} "DB users"]]]
     ;; Rows
     (doall
      (for [[i {:keys [resource role]}] (map-indexed vector all-roles)]
        (let [selected? (contains? (get selected-roles (:id resource)) (:name role))]
          ^{:key (str (:id resource) "-" (:name role))}
          [:> Flex {:px "3" :py "2" :align "center"
                    :on-click #(on-toggle (:id resource) (:name role))
                    :style {:border-bottom (when (< i (dec (count all-roles)))
                                             "1px solid var(--gray-3)")
                            :min-height 44
                            :background (cond
                                          selected? "var(--indigo-1)"
                                          (even? i) "var(--color-panel-solid)"
                                          :else "var(--gray-1)")
                            :cursor "pointer"}}
           [:> Box {:style {:width 36 :flex-shrink 0}
                    :on-click #(.stopPropagation %)}
            [:> Checkbox {:checked selected?
                          :onCheckedChange #(on-toggle (:id resource) (:name role))}]]
           [:> Flex {:align "center" :gap "2" :style {:flex "2 1 0"}}
            [:> Text {:size "2" :weight "medium"} (:name resource)]
            [:> Badge {:color "gray" :variant "soft" :size "1"} (:db-type resource)]]
           [:> Box {:style {:flex "2 1 0"}}
            [:> Text {:size "2" :style {:font-family "var(--font-mono)" :font-size 12}}
             (:name role)]]
           [:> Box {:style {:width 100 :flex-shrink 0}}
            [:> Badge {:color (get data/role-type-color (:type role) "gray")
                       :variant "soft" :size "1"}
             (:type role)]]
           [:> Box {:style {:width 80 :flex-shrink 0}}
            [:> Text {:size "2" :color "gray"} (:user-count role)]]])))]))

;; ── Main screen ────────────────────────────────────────────────────────────────
(defn bulk-roles-screen
  [{:keys [resources on-apply on-cancel initial-method]}]
  (let [method*         (r/atom (or initial-method "create"))
        csv-parsing     (r/atom false)
        csv-parsed      (r/atom false)
        discovered-roles (r/atom {})
        roles-loading   (r/atom false)
        selected-roles  (r/atom {})
        load-timer      (r/atom nil)]
    (fn []
      (let [method   @method*
            loading? @roles-loading

            total-discovered (reduce + 0 (map count (vals @discovered-roles)))
            total-selected   (reduce + 0 (map count (vals @selected-roles)))

            toggle-role (fn [resource-id role-name]
                          (swap! selected-roles
                                 (fn [sr]
                                   (let [s (get sr resource-id #{})]
                                     (assoc sr resource-id
                                            (if (contains? s role-name)
                                              (disj s role-name)
                                              (conj s role-name)))))))

            apply-disabled? (or (and (= method "csv") (not @csv-parsed))
                                (and (= method "bind") (or loading? (zero? total-selected))))

            footer-info (cond
                          (= method "create")
                          (str (* (count resources) 2) " roles will be created across "
                               (count resources) " resources")
                          (and (= method "bind") loading?)
                          "Reading roles from databases…"
                          (= method "bind")
                          (str total-discovered " roles discovered · " total-selected " selected")
                          @csv-parsed
                          (str (* (count resources) 2) " roles parsed from CSV")
                          :else "Upload a CSV to continue")]

        [:> Flex {:direction "column" :style {:flex 1 :min-height 0}}
         [:> Flex {:align "center" :gap "2" :mb "1"}
          [:> Button {:variant "ghost" :color "gray" :size "2" :on-click on-cancel}
           [:> ArrowLeft {:size 14}] " Back"]]
         [:> Flex {:align "baseline" :gap "3" :mb "5"}
          [:> Heading {:size "7"} "Provision — roles"]
          [:> Badge {:color "gray" :variant "soft"} (str (count resources) " resources")]]

         ;; Method cards
         [:> Flex {:gap "3" :mb "5"}
          [method-card {:selected (= method "create")
                        :icon     [:> Sparkles {:size 18}]
                        :title    "Create standard roles"
                        :description "Auto-create readonly and readwrite roles for each resource."
                        :badge    "Recommended"
                        :on-click (fn []
                                    (reset! method* "create")
                                    (when @load-timer (js/clearTimeout @load-timer)))}]
          [method-card {:selected (= method "bind")
                        :icon     [:> Key {:size 18}]
                        :title    "Bind existing roles"
                        :description "Read and select roles from the database — no new roles created."
                        :on-click (fn []
                                    (reset! method* "bind")
                                    (reset! roles-loading true)
                                    (reset! selected-roles {})
                                    (when @load-timer (js/clearTimeout @load-timer))
                                    (reset! load-timer
                                            (js/setTimeout
                                             (fn []
                                               (reset! discovered-roles
                                                       (into {} (map (fn [r]
                                                                       [(:id r) (data/get-mock-roles (:db-type r))])
                                                                     resources)))
                                               (reset! roles-loading false))
                                             1500)))}]
          [method-card {:selected (= method "csv")
                        :icon     [:> Upload {:size 18}]
                        :title    "Import from CSV"
                        :description "Define roles in a CSV file and bulk-apply across all resources."
                        :on-click (fn []
                                    (reset! method* "csv")
                                    (when @load-timer (js/clearTimeout @load-timer)))}]]

         ;; ── Create mode ──
         (when (= method "create")
           [:> Box {:style {:flex 1 :overflow-y "auto"
                            :border "1px solid var(--gray-5)"
                            :border-radius "var(--radius-2)"}}
            [:> Flex {:px "3" :py "2"
                      :style {:background "var(--gray-3)"
                              :border-bottom "1px solid var(--gray-5)"}}
             [:> Box {:style {:flex 1}}
              [:> Text {:size "1" :color "gray" :weight "medium"} "Resource"]]
             [:> Box {:style {:flex 1}}
              [:> Text {:size "1" :color "gray" :weight "medium"} "Roles to create"]]]
            (doall
             (for [[i r] (map-indexed vector resources)]
               ^{:key (:id r)}
               [:> Flex {:px "3" :py "2" :align "center"
                         :style {:border-bottom (when (< i (dec (count resources)))
                                                  "1px solid var(--gray-3)")
                                 :min-height 44
                                 :background (if (even? i)
                                               "var(--color-panel-solid)" "var(--gray-1)")}}
                [:> Flex {:align "center" :gap "2" :style {:flex 1}}
                 [:> Text {:size "2" :weight "medium"} (:name r)]
                 [:> Badge {:color "gray" :variant "soft" :size "1"} (:db-type r)]
                 [:> Text {:size "1" :color "gray"
                           :style {:font-family "var(--font-mono)" :font-size 11}}
                  (:host r)]]
                [:> Flex {:gap "2" :style {:flex 1}}
                 [:> Badge {:color "indigo" :variant "soft" :size "1"}
                  (str (:name r) "-readonly")]
                 [:> Badge {:color "indigo" :variant "soft" :size "1"}
                  (str (:name r) "-readwrite")]]]))])

         ;; ── Bind mode — loading ──
         (when (and (= method "bind") loading?)
           [:> Box {:style {:flex 1 :border "1px solid var(--gray-5)"
                            :border-radius "var(--radius-2)" :overflow "hidden"}}
            [:> Flex {:px "3" :py "2"
                      :style {:background "var(--gray-3)"
                              :border-bottom "1px solid var(--gray-5)"}}
             [:> Text {:size "1" :color "gray" :weight "medium"}
              (str "Reading roles from " (count resources) " resources…")]]
            (for [i (range 8)]
              ^{:key i}
              [:> Flex {:px "3" :py "3" :align "center" :gap "3"
                        :style {:border-bottom (when (< i 7) "1px solid var(--gray-3)")}}
               [:> Skeleton {:width "20px" :height "16px"}]
               [:> Skeleton {:width "140px" :height "14px"}]
               [:> Skeleton {:width "120px" :height "14px"}]
               [:> Skeleton {:width "70px" :height "20px"}]
               [:> Skeleton {:width "50px" :height "14px"}]])])

         ;; ── Bind mode — role selection ──
         (when (and (= method "bind") (not loading?))
           [:<>
            [:> Callout.Root {:color "blue" :mb "3" :size "1"}
             [:> Callout.Icon [:> Info {:size 14}]]
             [:> Callout.Text {:size "1"}
              "Select the roles to bring into Hoop. A Hoop user will be created for each selected role and bound to the existing database role — no new roles are created in the database."]]
            [role-discovery-table
             {:resources        resources
              :discovered-roles @discovered-roles
              :selected-roles   @selected-roles
              :on-toggle        toggle-role}]])

         ;; ── CSV mode — upload ──
         (when (and (= method "csv") (not @csv-parsed))
           [:> Flex {:direction "column" :gap "3" :style {:flex 1}}
            [:> Box {:on-click (fn []
                                 (reset! csv-parsing true)
                                 (js/setTimeout (fn []
                                                  (reset! csv-parsing false)
                                                  (reset! csv-parsed true))
                                                900))
                     :style {:border "2px dashed var(--gray-6)"
                             :border-radius "var(--radius-3)"
                             :padding 40 :background "var(--gray-2)"
                             :text-align "center" :cursor "pointer"
                             :flex 1 :display "flex" :align-items "center"
                             :justify-content "center"}}
             (if @csv-parsing
               [:> Flex {:direction "column" :align "center" :gap "2"}
                [:span {:class "animate-spin inline-flex" :style {:color "var(--indigo-9)"}}
                 [:> Loader2 {:size 20}]]
                [:> Text {:size "2" :color "gray"} "Parsing CSV…"]]
               [:> Flex {:direction "column" :align "center" :gap "2"}
                [:> Upload {:size 24 :stroke-width 1.5 :color "var(--gray-9)"}]
                [:> Text {:size "2" :color "gray"}
                 "Drop your CSV here or "
                 [:> Text {:size "2" :color "indigo" :style {:cursor "pointer"}} "browse"]]
                [:> Text {:size "1" :color "gray"} "Columns: resource_name, role_name, permissions"]])]
            [:> Flex {:justify "end"}
             [:> Button {:variant "ghost" :size "1" :color "gray"}
              [:> FileText {:size 12}] " Download template"]]])

         ;; ── CSV mode — parsed preview ──
         (when (and (= method "csv") @csv-parsed)
           [:> Box {:style {:flex 1 :overflow-y "auto"
                            :border "1px solid var(--gray-5)"
                            :border-radius "var(--radius-2)"}}
            [:> Flex {:px "3" :py "2"
                      :style {:background "var(--gray-3)"
                              :border-bottom "1px solid var(--gray-5)"}}
             [:> Box {:style {:flex 1}}
              [:> Text {:size "1" :color "gray" :weight "medium"} "Resource"]]
             [:> Box {:style {:flex 1}}
              [:> Text {:size "1" :color "gray" :weight "medium"} "Role name"]]
             [:> Box {:style {:width 180 :flex-shrink 0}}
              [:> Text {:size "1" :color "gray" :weight "medium"} "Permissions"]]
             [:> Box {:style {:width 70}}
              [:> Text {:size "1" :color "gray" :weight "medium"} "Status"]]]
            (let [rows (mapcat (fn [r]
                                 [{:key (str (:id r) "-ro") :name (:name r) :type (:db-type r)
                                   :role (str (:name r) "_readonly") :perms "SELECT"}
                                  {:key (str (:id r) "-rw") :name (:name r) :type (:db-type r)
                                   :role (str (:name r) "_readwrite") :perms "SELECT, INSERT, UPDATE"}])
                               resources)]
              (doall
               (for [[i row] (map-indexed vector rows)]
                 ^{:key (:key row)}
                 [:> Flex {:px "3" :py "2" :align "center"
                           :style {:border-bottom (when (< i (dec (count rows)))
                                                    "1px solid var(--gray-3)")
                                   :min-height 40
                                   :background (if (even? i)
                                                 "var(--color-panel-solid)" "var(--gray-1)")}}
                  [:> Flex {:align "center" :gap "2" :style {:flex 1}}
                   [:> Text {:size "2"} (:name row)]
                   [:> Badge {:color "gray" :variant "soft" :size "1"} (:type row)]]
                  [:> Box {:style {:flex 1}}
                   [:> Text {:size "2" :style {:font-family "var(--font-mono)" :font-size 12}}
                    (:role row)]]
                  [:> Box {:style {:width 180 :flex-shrink 0}}
                   [:> Text {:size "1" :color "gray"} (:perms row)]]
                  [:> Badge {:color "green" :variant "soft" :size "1"}
                   [:> Check {:size 10}] " Valid"]])))])

         ;; Footer
         [:> Flex {:align "center" :justify "between" :pt "4" :mt "4"
                   :style {:border-top "1px solid var(--gray-4)" :flex-shrink 0}}
          [:> Text {:size "1" :color "gray"} footer-info]
          [:> Flex {:gap "3"}
           [:> Button {:variant "outline" :color "gray" :on-click on-cancel} "Cancel"]
           [:> Button {:disabled apply-disabled?
                       :on-click (fn []
                                   (let [roles-by-resource
                                         (when (= method "bind")
                                           (into {}
                                                 (map (fn [[id s]] [id (vec s)])
                                                      @selected-roles)))]
                                     (on-apply method roles-by-resource)))}
            (if (= method "bind")
              (str "Bind " total-selected " role"
                   (when (not= 1 total-selected) "s") " →")
              (str "Provision " (count resources)
                   (if (= 1 (count resources)) " resource" " resources") " →"))]]]]))))
