(ns webapp.features.access-request.views.rule-form
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Switch Text]]
   ["lucide-react" :refer [ClockArrowUp CodeXml]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.button :as button]
   [webapp.components.connections-select :as connections-select]
   [webapp.components.forms :as forms]
   [webapp.components.loaders :as loaders]
   [webapp.components.multiselect :as multiselect]
   [webapp.components.selection-card :refer [selection-card]]
   [webapp.features.access-request.views.free-license-callout :refer [free-license-callout]]))

(def time-range-options
  [{:text "15 minutes" :value 900}
   {:text "30 minutes" :value 1800}
   {:text "1 hour" :value 3600}
   {:text "2 hours" :value 7200}
   {:text "4 hours" :value 14400}
   {:text "8 hours" :value 28800}
   {:text "16 hours" :value 57600}
   {:text "24 hours" :value 86400}
   {:text "32 hours" :value 115200}
   {:text "40 hours" :value 144000}
   {:text "48 hours" :value 172800}])

(defn- format-user-groups [groups]
  (mapv (fn [group]
          {:value group
           :label group})
        groups))

(defn- array->select-options [items]
  (mapv (fn [item]
          {:value item :label item})
        items))

(defn- create-form-state [initial-data]
  (let [rule-data (or initial-data {})]
    {:rule-name (r/atom (or (:name rule-data) ""))
     :description (r/atom (or (:description rule-data) ""))
     :access-type (r/atom (or (:access_type rule-data) "command"))
     :access-duration (r/atom (:access_max_duration rule-data))
     :connection-names (r/atom (or (:connection_names rule-data) []))
     :approval-required-groups (r/atom (or (array->select-options (:approval_required_groups rule-data)) []))
     :all-groups-must-approve (r/atom (if (some? (:all_groups_must_approve rule-data))
                                        (:all_groups_must_approve rule-data)
                                        true))
     :reviewers-groups (r/atom (or (array->select-options (:reviewers_groups rule-data)) []))
     :force-approval-groups (r/atom (or (array->select-options (:force_approval_groups rule-data)) []))
     :min-approvals (r/atom (if (:min_approvals rule-data)
                              (str (:min_approvals rule-data))
                              ""))}))

(defn- form-section [{:keys [title description]} & children]
  [:> Grid {:columns "7" :gap "7"}
   [:> Box {:grid-column "span 2 / span 2"}
    [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
     title]
    [:> Text {:size "3" :class "text-[--gray-11]"}
     description]]
   (into [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}]
         children)])

(defn rule-form [form-type rule-data scroll-pos]
  (let [state (create-form-state rule-data)
        connections (rf/subscribe [:connections->pagination])
        user-groups (rf/subscribe [:user-groups])
        current-user (rf/subscribe [:users->current-user])
        existing-rules (rf/subscribe [:access-request/rules])]
    (fn []
      (let [user-groups-options (format-user-groups (or @user-groups []))
            free-license? (get-in @current-user [:data :free-license?])
            can-create? (or (not free-license?) (< (count (or @existing-rules [])) 1))
            rule-name @(:rule-name state)]

        [:> Box {:class "min-h-screen bg-gray-1"}
         [:form {:on-submit (fn [e]
                              (.preventDefault e)
                              (when (or (= form-type :edit) can-create?)
                                (let [rule-data (cond-> {:name @(:rule-name state)
                                                         :access_type @(:access-type state)
                                                         :connection_names @(:connection-names state)
                                                         :approval_required_groups (mapv :value @(:approval-required-groups state))
                                                         :all_groups_must_approve @(:all-groups-must-approve state)
                                                         :reviewers_groups (mapv :value @(:reviewers-groups state))
                                                         :force_approval_groups (mapv :value @(:force-approval-groups state))
                                                         :min_approvals (js/parseInt @(:min-approvals state))}
                                                  (seq @(:description state))
                                                  (assoc :description @(:description state))

                                                  (and (= @(:access-type state) "jit") @(:access-duration state))
                                                  (assoc :access_max_duration @(:access-duration state)))]
                                  (if (= form-type :create)
                                    (rf/dispatch [:access-request/create-rule rule-data])
                                    (rf/dispatch [:access-request/update-rule rule-name rule-data])))))}

          [:<>
           [:> Flex {:p "5" :gap "2"}
            [button/HeaderBack]]
           [:> Box {:class (str "sticky top-0 z-50 bg-gray-1 px-7 py-7 "
                                (when (>= @scroll-pos 30)
                                  "border-b border-[--gray-a6]"))}
            [:> Flex {:justify "between"
                      :align "center"}
             [:> Heading {:as "h2" :size "8"}
              (if (= form-type :create)
                "Create new Access Request rule"
                "Edit Access Request rule")]
             [:> Flex {:gap "5" :align "center"}
              (when (= form-type :edit)
                [:> Button {:size "4"
                            :variant "ghost"
                            :color "red"
                            :type "button"
                            :on-click #(rf/dispatch [:dialog->open
                                                     {:title "Delete Rule"
                                                      :text (str "Are you sure you want to delete the rule '" rule-name "'? This action cannot be undone.")
                                                      :text-action-button "Delete"
                                                      :action-button? true
                                                      :type :danger
                                                      :on-success (fn []
                                                                    (rf/dispatch [:access-request/delete-rule rule-name]))}])}
                 "Delete"])
              [:> Button {:size "3"
                          :disabled (and (= form-type :create) (not can-create?))
                          :type "submit"}
               "Save"]]]]]

          (when free-license?
            [:> Box {:class "mx-7 mt-4"}
             [free-license-callout]])

          [:> Box {:p "7" :class "space-y-radix-9"}
           [form-section {:title "Set new rule information"
                          :description "Used to identify your Access Request rule in your connections."}
            [forms/input
             (cond-> {:label "Name"
                      :value @(:rule-name state)
                      :required true
                      :class "w-full"}
               (= form-type :create) (assoc :placeholder "e.g. data-engineering"
                                            :autoFocus true
                                            :on-change #(reset! (:rule-name state) (-> % .-target .-value)))
               (= form-type :edit) (assoc :disabled true))]
            [forms/input
             {:placeholder (if (= form-type :create)
                             "Describe how this is used in your connections"
                             "e.g. Time-based database access for Marketing teams")
              :label "Description (Optional)"
              :value @(:description state)
              :class "w-full"
              :on-change #(reset! (:description state) (-> % .-target .-value))}]]

           [form-section {:title "Request access type"
                          :description "Define how to request to your connections."}
            [:> Flex {:direction "column" :gap "4"}
             [selection-card
              {:icon (r/as-element [:> ClockArrowUp {:size 20}])
               :title "Just-in-Time"
               :description "For temporary access expiring automatically after defined time range"
               :selected? (= @(:access-type state) "jit")
               :on-click #(reset! (:access-type state) "jit")}]
             [selection-card
              {:icon (r/as-element [:> CodeXml {:size 20}])
               :title "by Command"
               :description "For execution-based with approval workflow"
               :selected? (= @(:access-type state) "command")
               :on-click #(reset! (:access-type state) "command")}]]]

           (when (= @(:access-type state) "jit")
             [form-section {:title "Access time range"
                            :description "Select for how long temporary access will be available for your connections."}
              (let [time-range-opts (mapv #(into {} {"value" (:value %) "label" (:text %)})
                                          time-range-options)
                    selected-option (when @(:access-duration state)
                                      (if (map? @(:access-duration state))
                                        @(:access-duration state)
                                        (first (filter #(= (get % "value") @(:access-duration state)) time-range-opts))))]
                [multiselect/single
                 {:options time-range-opts
                  :label "Time Range"
                  :id "access-duration-input"
                  :name "access-duration-input"
                  :default-value selected-option
                  :clearable? true
                  :required? true
                  :on-change #(let [selected (js->clj %)]
                                (reset! (:access-duration state) (when selected (get selected "value"))))}])])

           [form-section {:title "Resource configuration"
                          :description "Select which connections to apply this configuration."}
            (let [conns (or (:data @connections) [])
                  conn-by-name (into {} (map (juxt :name identity)) conns)
                  selected-connection-names @(:connection-names state)
                  selected-connections-data (mapv (fn [name]
                                                    (let [conn (get conn-by-name name)]
                                                      {:id (or (:id conn) name)
                                                       :name name}))
                                                  selected-connection-names)
                  connection-ids (mapv (fn [name]
                                         (or (:id (get conn-by-name name)) name))
                                       selected-connection-names)]
              [connections-select/main
               {:connection-ids connection-ids
                :selected-connections selected-connections-data
                :on-connections-change (fn [selected-options]
                                         (let [selected-js-options (js->clj selected-options :keywordize-keys true)
                                               selected-names (mapv #(:label %) selected-js-options)]
                                           (reset! (:connection-names state) selected-names)))}])]

           [form-section {:title "Required user groups"
                          :description "Select which user groups are required to request access with this rule."}
            [multiselect/main
             {:label "User Groups"
              :options user-groups-options
              :default-value @(:approval-required-groups state)
              :placeholder "Select groups..."
              :on-change #(reset! (:approval-required-groups state) (js->clj % :keywordize-keys true))}]]

           [form-section {:title "Approval user groups"
                          :description "Select which user groups can approve access in this rule."}
            [multiselect/main
             {:label "User Groups"
              :options user-groups-options
              :default-value @(:reviewers-groups state)
              :placeholder "Select groups..."
              :on-change #(reset! (:reviewers-groups state) (js->clj % :keywordize-keys true))}]
            [:> Flex {:align "center" :gap "3" :class "pt-4"}
             [:> Switch {:checked @(:all-groups-must-approve state)
                         :size "3"
                         :onCheckedChange (fn [checked]
                                            (reset! (:all-groups-must-approve state) checked)
                                            (when (and (not checked) (empty? @(:min-approvals state)))
                                              (reset! (:min-approvals state) "1")))}]
             [:> Box
              [:> Text {:size "2" :weight "bold" :class "block"}
               "Require all groups approval"]
              [:> Text {:size "2" :class "text-[--gray-11]"}
               "Request additional approval from at least one member of each group"]]]]

           [form-section {:title "Approval amount"
                          :description "Define the minimum number of approvals required for each session."}
            [forms/input
             {:type "number"
              :placeholder "e.g. 2"
              :label "Minimum Approval Amount"
              :value (or @(:min-approvals state) "")
              :class "w-full"
              :min 1
              :required (not @(:all-groups-must-approve state))
              :disabled @(:all-groups-must-approve state)
              :on-change #(reset! (:min-approvals state) (-> % .-target .-value))}]]

           [form-section {:title "Force approval groups (Optional)"
                          :description "Select which user groups are allowed to bypass other approval rules."}
            [multiselect/main
             {:label "User Groups"
              :options user-groups-options
              :default-value @(:force-approval-groups state)
              :placeholder "Select groups..."
              :on-change #(reset! (:force-approval-groups state) (js->clj % :keywordize-keys true))}]]]]]))))

(defn main [mode & [params]]
  (let [current-rule (rf/subscribe [:access-request/current-rule])
        status (rf/subscribe [:access-request/status])
        scroll-pos (r/atom 0)]

    (rf/dispatch [:users->get-user-groups])
    (rf/dispatch [:connections/get-connections-paginated {:force-refresh? true}])

    (when (= :edit mode)
      (rf/dispatch [:access-request/get-rule (:rule-name params)]))

    (fn []
      (r/with-let [handle-scroll #(reset! scroll-pos (.-scrollY js/window))
                   _ (.addEventListener js/window "scroll" handle-scroll)]
        (let [rule-data (if (= :edit mode) @current-rule {})]
          (if (and (= :edit mode) (= :loading @status))
            [:> Box {:class "bg-gray-1 h-full"}
             [:> Flex {:direction "column" :justify "center" :align "center" :height "100%"}
              [loaders/simple-loader]]]
            ^{:key (str mode "-" (:name rule-data))}
            [rule-form mode rule-data scroll-pos]))
        (finally
          (.removeEventListener js/window "scroll" handle-scroll)
          (rf/dispatch [:access-request/clear-current-rule]))))))
