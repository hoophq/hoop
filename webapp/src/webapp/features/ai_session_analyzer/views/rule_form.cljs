(ns webapp.features.ai-session-analyzer.views.rule-form
  (:require
   ["@radix-ui/themes" :refer [Avatar Badge Box Button Callout Card Flex Grid Heading Text]]
   ["lucide-react" :refer [ArrowLeft Check Info X]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.components.loaders :as loaders]
   [webapp.components.connections-select :as connections-select]))

(def risk-levels
  [{:key :low
    :label "Low risk"
    :description "Activities that appear unlikely to cause significant system, data, or security impact based on intent and structure."
    :recommended "allow_execution"}
   {:key :medium
    :label "Medium risk"
    :description "Activities that may modify data, configuration, or runtime behavior in a scoped or limited way."
    :recommended "allow_execution"}
   {:key :high
    :label "High risk"
    :description "Activities that suggest potentially destructive, irreversible, privilege-altering, or security-sensitive behavior."
    :recommended "block_execution"}])

(defn- risk-selection-card
  [{:keys [icon title description selected? on-click recommended? icon-class]}]
  [:> Card {:size "1"
            :variant "surface"
            :class (str "w-full cursor-pointer "
                        (when selected? "before:bg-primary-12"))
            :on-click on-click}
   [:> Flex {:align "center" :justify "between" :gap "3" :class (str (when selected? "text-[--gray-1]"))}
    [:> Flex {:align "center" :gap "3"}
     (when icon
       [:> Avatar {:size "4"
                   :class (str (when selected? "dark ")
                               (or icon-class ""))
                   :variant "soft"
                   :color "gray"
                   :fallback icon}])
     [:> Flex {:direction "column"}
      [:> Text {:size "3" :weight "medium" :color "gray-12"} title]
      (when description
        [:> Text {:size "2" :class (if selected? "text-[--gray-1]" "text-[--gray-11]")} description])]]
    (when recommended?
      [:> Badge {:variant (if selected? "surface" "soft") :color "indigo"}
       "Recommended"])]])

(defn- risk-level-section
  [{:keys [risk-level action-atom]}]
  [:> Grid {:columns "7" :gap "7"}
   [:> Box {:grid-column "span 2 / span 2"}
    [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
     (:label risk-level)]
    [:> Text {:size "3" :class "text-[--gray-11]"}
     (:description risk-level)]]

   [:> Box {:grid-column "span 5 / span 5" :class "space-y-radix-3"}
    [risk-selection-card
     {:icon (r/as-element [:> Check {:size 16}])
      :title "Allow execution"
      :description "User activity will proceed normally."
      :selected? (= @action-atom "allow_execution")
      :recommended? (= (:recommended risk-level) "allow_execution")
      :on-click #(reset! action-atom "allow_execution")}]
    [risk-selection-card
     {:icon (r/as-element [:> X {:size 16}])
      :title "Block execution"
      :description "User activity will be blocked."
      :selected? (= @action-atom "block_execution")
      :recommended? (= (:recommended risk-level) "block_execution")
      :on-click #(reset! action-atom "block_execution")}]]])

(defn- create-form-state [initial-data]
  (let [rule (or initial-data {})
        risk (or (:risk_evaluation rule) {})]
    {:name (r/atom (or (:name rule) ""))
     :description (r/atom (or (:description rule) ""))
     :connection-names (r/atom (or (:connection_names rule) []))
     :low-risk-action (r/atom (or (:low_risk_action risk) "allow_execution"))
     :medium-risk-action (r/atom (or (:medium_risk_action risk) "allow_execution"))
     :high-risk-action (r/atom (or (:high_risk_action risk) "block_execution"))}))

(defn rule-form [form-type rule-data scroll-pos]
  (let [state (create-form-state rule-data)
        rule-loading? (rf/subscribe [:ai-session-analyzer/rule-loading?])
        connections (rf/subscribe [:connections->pagination])]
    (fn []
      (let [selected-connection-names @(:connection-names state)
            conns (or (:data @connections) [])
            conn-by-name (into {} (map (juxt :name identity)) conns)
            selected-connections-data (mapv (fn [name]
                                              (let [conn (get conn-by-name name)]
                                                {:id (or (:id conn) name)
                                                 :name name}))
                                            selected-connection-names)
            connection-ids (mapv (fn [name]
                                   (or (:id (get conn-by-name name)) name))
                                 selected-connection-names)]
        [:> Box {:class "min-h-screen bg-gray-1"}
         [:form {:id "ai-session-analyzer-rule-form"
                 :on-submit (fn [e]
                              (.preventDefault e)
                              (let [payload {:name @(:name state)
                                             :description (when (seq @(:description state))
                                                            @(:description state))
                                             :connection_names @(:connection-names state)
                                             :risk_evaluation {:low_risk_action @(:low-risk-action state)
                                                               :medium_risk_action @(:medium-risk-action state)
                                                               :high_risk_action @(:high-risk-action state)}}]
                                (if (= :edit form-type)
                                  (rf/dispatch [:ai-session-analyzer/update-rule @(:name state) payload])
                                  (rf/dispatch [:ai-session-analyzer/create-rule payload]))))}

          [:<>
           [:> Flex {:p "5" :gap "2"}
            [:> Button {:variant "ghost"
                        :size "2"
                        :color "gray"
                        :type "button"
                        :on-click #(rf/dispatch [:navigate :ai-session-analyzer])}
             [:> ArrowLeft {:size 16}]
             "Back"]]
           [:> Box {:class (str "sticky top-0 z-50 bg-gray-1 px-7 py-7 "
                                (when (>= @scroll-pos 30)
                                  "border-b border-[--gray-a6]"))}
            [:> Flex {:justify "between" :align "center"}
             [:> Heading {:as "h2" :size "8"}
              (if (= :edit form-type)
                "Edit AI Session Analyzer rule"
                "Create new AI Session Analyzer rule")]
             [:> Flex {:gap "5" :align "center"}
              (when (= :edit form-type)
                [:> Button {:size "4"
                            :variant "ghost"
                            :color "red"
                            :type "button"
                            :disabled @rule-loading?
                            :on-click (fn []
                                        (rf/dispatch
                                         [:dialog->open
                                          {:title "Delete rule"
                                           :text (str "Are you sure you want to delete the rule \"" @(:name state) "\"? This action cannot be undone.")
                                           :text-action-button "Delete"
                                           :action-button? true
                                           :type :danger
                                           :on-success (fn []
                                                         (rf/dispatch [:ai-session-analyzer/delete-rule @(:name state)]))}]))}
                 "Delete"])
              [:> Button {:size "3"
                          :loading @rule-loading?
                          :disabled @rule-loading?
                          :type "submit"}
               "Save"]]]]]

          [:> Box {:p "7" :class "space-y-radix-9"}

           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Heading {:as "h3" :size "4"} "Define details"]
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "Used to identify your AI Session Analyzer rule in your resources."]]
            [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
             [forms/input
              {:label "Name"
               :placeholder "e.g. Rule-Name-1"
               :required true
               :value @(:name state)
               :on-change #(reset! (:name state) (-> % .-target .-value))}]
             [forms/input
              {:label "Description (Optional)"
               :placeholder "Describe how this rule is used"
               :required false
               :value @(:description state)
               :on-change #(reset! (:description state) (-> % .-target .-value))}]]]

           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Heading {:as "h3" :size "4"} "Roles configuration"]
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "Select which Resources to apply this configuration."]]
            [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
             [connections-select/main
              {:connection-ids connection-ids
               :selected-connections selected-connections-data
               :on-connections-change (fn [selected-options]
                                        (let [selected-js-options (js->clj selected-options :keywordize-keys true)
                                              selected-names (mapv #(:label %) selected-js-options)]
                                          (reset! (:connection-names state) selected-names)))}]]]

           [:> Box {:class "space-y-radix-7"}
            [:> Grid {:columns "7" :gap "7"}
             [:> Box {:grid-column "span 7 / span 7"}
              [:> Heading {:as "h3" :size "4"} "Risk evaluation"]
              [:> Text {:size "3" :class "text-[--gray-11]"}
               "Define policies by risk level and define actions at session time."]
              [:> Callout.Root {:size "1" :color "accent" :class "mt-4" :highContrast true}
               [:> Callout.Icon
                [:> Info {:size 16 :style {:color "var(--accent-10)"}}]]
               [:> Callout.Text
                "Recommended policies are calibrated to the session's risk profile"]]]]

            (for [level risk-levels]
              ^{:key (name (:key level))}
              [risk-level-section
               {:risk-level level
                :action-atom (case (:key level)
                               :low (:low-risk-action state)
                               :medium (:medium-risk-action state)
                               :high (:high-risk-action state))}])]]]]))))

(defn loading-view []
  [:> Flex {:justify "center" :align "center" :class "rounded-lg border bg-white h-full"}
   [loaders/simple-loader {:size "6" :border-size "4"}]])

(defn main [form-type & [params]]
  (let [active-rule (rf/subscribe [:ai-session-analyzer/active-rule])
        scroll-pos (r/atom 0)]

    (rf/dispatch [:connections/get-connections-paginated {:force-refresh? true}])

    (when (and (= :edit form-type) (:rule-name params))
      (rf/dispatch [:ai-session-analyzer/get-rule-by-name (:rule-name params)]))

    (fn []
      (r/with-let [handle-scroll #(reset! scroll-pos (.-scrollY js/window))]
        (.addEventListener js/window "scroll" handle-scroll)

        (if (and (= :edit form-type)
                 (= :loading (:status @active-rule)))
          [loading-view]
          [rule-form form-type
           (if (= :edit form-type)
             (:data @active-rule)
             {})
           scroll-pos])

        (finally
          (.removeEventListener js/window "scroll" handle-scroll)
          (when (= :edit form-type)
            (rf/dispatch [:ai-session-analyzer/clear-active-rule])))))))
