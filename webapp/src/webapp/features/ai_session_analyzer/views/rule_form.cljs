(ns webapp.features.ai-session-analyzer.views.rule-form
  (:require
   ["@radix-ui/themes" :refer [Avatar Badge Box Button Callout Card Flex Grid Heading IconButton Text Tooltip]]
   ["lucide-react" :refer [ArrowLeft Check ChevronDown ChevronUp Copy Info ShieldCheck Sparkles X]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.components.loaders :as loaders]
   [webapp.components.connections-select :as connections-select]
   [webapp.features.activation-journey.views.enterprise-banner :as enterprise-banner]
   [webapp.features.promotion :as promotion]))

(def risk-levels
  [{:key :low
    :label "Low risk"
    :description "Activities that appear unlikely to cause significant system, data, or security impact based on intent and structure."
    :recommended "allow_execution"}
   {:key :medium
    :label "Medium risk"
    :description "Activities that may modify data, configuration, or runtime behavior in a scoped or limited way."
    :recommended "require_access_request"}
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
  [{:keys [risk-level action-atom rule-name-atom access-request-rules]}]
  (let [action @action-atom
        rule-options (mapv (fn [r] {:value (:name r) :text (:name r)})
                           (or access-request-rules []))]
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
        :selected? (= action "allow_execution")
        :recommended? (= (:recommended risk-level) "allow_execution")
        :on-click (fn []
                    (reset! action-atom "allow_execution")
                    (reset! rule-name-atom nil))}]
      [risk-selection-card
       {:icon (r/as-element [:> X {:size 16}])
        :title "Block execution"
        :description "User activity will be blocked."
        :selected? (= action "block_execution")
        :recommended? (= (:recommended risk-level) "block_execution")
        :on-click (fn []
                    (reset! action-atom "block_execution")
                    (reset! rule-name-atom nil))}]
      [risk-selection-card
       {:icon (r/as-element [:> ShieldCheck {:size 16}])
        :title "Require access request"
        :description "User activity will wait for an access request approval before running."
        :selected? (= action "require_access_request")
        :recommended? (= (:recommended risk-level) "require_access_request")
        :on-click #(reset! action-atom "require_access_request")}]
      (when (= action "require_access_request")
        [:> Box {:class "pl-4"}
         (if (empty? rule-options)
           [:> Callout.Root {:size "1" :color "amber"}
            [:> Callout.Icon
             [:> Info {:size 16}]]
            [:> Callout.Text
             "No access request rules are configured. Create one in Access Control before selecting this action."]]
           [forms/select
            {:label "Access request rule"
             :required true
             :full-width? true
             :not-margin-bottom? true
             :placeholder "Select an access request rule"
             :options rule-options
             :selected (or @rule-name-atom "")
             :on-change #(reset! rule-name-atom %)}])])]]))

(defn- tier-from-risk [risk level-key legacy-key default-action]
  (let [tier (get risk level-key)
        action (or (:action tier)
                   (get risk legacy-key)
                   default-action)
        rule-name (:access_request_rule_name tier)]
    [action rule-name]))

(def ^:private recommended-by-key
  (into {} (map (juxt :key :recommended)) risk-levels))

(defn- create-form-state [initial-data]
  (let [rule (or initial-data {})
        risk (or (:risk_evaluation rule) {})
        [low-action low-rule] (tier-from-risk risk :low_risk :low_risk_action (recommended-by-key :low))
        [medium-action medium-rule] (tier-from-risk risk :medium_risk :medium_risk_action (recommended-by-key :medium))
        [high-action high-rule] (tier-from-risk risk :high_risk :high_risk_action (recommended-by-key :high))]
    {:name (r/atom (or (:name rule) ""))
     :description (r/atom (or (:description rule) ""))
     :connection-names (r/atom (or (:connection_names rule) []))
     :custom-prompt (r/atom (or (:custom_prompt rule) ""))
     :low-risk-action (r/atom low-action)
     :low-risk-rule (r/atom low-rule)
     :medium-risk-action (r/atom medium-action)
     :medium-risk-rule (r/atom medium-rule)
     :high-risk-action (r/atom high-action)
     :high-risk-rule (r/atom high-rule)}))

(defn- build-tier [action-atom rule-name-atom]
  (let [action @action-atom
        rule-name @rule-name-atom
        tier {:action action}]
    (if (and (= action "require_access_request") (seq rule-name))
      (assoc tier :access_request_rule_name rule-name)
      tier)))

(defn- system-prompt-preview []
  (let [expanded? (r/atom false)
        prompt-sub (rf/subscribe [:ai-session-analyzer/system-prompt])]
    (rf/dispatch [:ai-session-analyzer/get-system-prompt])
    (fn []
      (let [{:keys [status data]} @prompt-sub
            prompt-text (or data "")
            loading? (= status :loading)
            errored? (= status :error)]
        [:> Box {:class "border border-[--gray-a6] rounded-3 bg-gray-2 overflow-hidden"}
         [:button {:type "button"
                   :on-click #(swap! expanded? not)
                   :aria-expanded @expanded?
                   :class "w-full flex items-center justify-between bg-transparent border-none px-4 py-3 cursor-pointer"}
          [:> Flex {:align "center" :gap "2"}
           [:> Sparkles {:size 14 :class "text-[--indigo-9]"}]
           [:> Text {:size "2" :weight "medium" :class "text-[--gray-12]"}
            "Hoop's appended system prompt"]
           [:> Badge {:variant "soft" :color "gray" :radius "full" :size "1"}
            "Read-only"]]
          (if @expanded?
            [:> ChevronUp {:size 14 :class "text-[--gray-11]"}]
            [:> ChevronDown {:size 14 :class "text-[--gray-11]"}])]
         (when @expanded?
           [:> Box {:class "border-t border-[--gray-a6] bg-[--gray-12] text-[--gray-2] relative"
                    :style {:padding "16px 18px"}}
            (cond
              loading?
              [:> Text {:size "2" :class "text-[--gray-3]"} "Loading prompt..."]

              errored?
              [:> Text {:size "2" :class "text-[--red-9]"}
               "Failed to load the system prompt. Refresh and try again."]

              :else
              [:<>
               [:> Box {:class "absolute right-3 top-3"}
                [:> Tooltip {:content "Copy to clipboard"}
                 [:> IconButton {:size "1"
                                 :variant "soft"
                                 :color "gray"
                                 :type "button"
                                 :on-click (fn [e]
                                             (.stopPropagation e)
                                             (-> js/navigator
                                                 .-clipboard
                                                 (.writeText prompt-text))
                                             (rf/dispatch [:show-snackbar
                                                           {:level :success
                                                            :text "System prompt copied to clipboard"}]))}
                  [:> Copy {:size 12}]]]]
               [:pre {:class "m-0 whitespace-pre-wrap text-[--gray-3]"
                      :style {:font-family "var(--font-mono)"
                              :font-size "12px"
                              :line-height "1.6"}}
                prompt-text]])])]))))

(defn rule-form [form-type rule-data scroll-pos]
  (let [state (create-form-state rule-data)
        rule-loading? (rf/subscribe [:ai-session-analyzer/rule-loading?])
        connections (rf/subscribe [:connections->pagination])
        access-rules-sub (rf/subscribe [:access-request/rules])
        user (rf/subscribe [:users->current-user])]
    (rf/dispatch [:access-request/list-rules])
    (fn []
      (let [free-license? (-> @user :data :free-license?)
            selected-connection-names @(:connection-names state)
            conns (or (:data @connections) [])
            conn-by-name (into {} (map (juxt :name identity)) conns)
            selected-connections-data (mapv (fn [name]
                                              (let [conn (get conn-by-name name)]
                                                {:id (or (:id conn) name)
                                                 :name name}))
                                            selected-connection-names)
            connection-ids (mapv (fn [name]
                                   (or (:id (get conn-by-name name)) name))
                                 selected-connection-names)
            access-rules (or @access-rules-sub [])
            low-tier (build-tier (:low-risk-action state) (:low-risk-rule state))
            medium-tier (build-tier (:medium-risk-action state) (:medium-risk-rule state))
            high-tier (build-tier (:high-risk-action state) (:high-risk-rule state))
            require-rule-missing? (some (fn [tier]
                                          (and (= (:action tier) "require_access_request")
                                               (not (seq (:access_request_rule_name tier)))))
                                        [low-tier medium-tier high-tier])]
        [:> Box {:class "min-h-screen bg-gray-1"}
         [:form {:id "ai-session-analyzer-rule-form"
                 :on-submit (fn [e]
                              (.preventDefault e)
                              (when-not require-rule-missing?
                                (let [trimmed-prompt (when (seq @(:custom-prompt state))
                                                       (.trim @(:custom-prompt state)))
                                      payload {:name @(:name state)
                                               :description (when (seq @(:description state))
                                                              @(:description state))
                                               :connection_names @(:connection-names state)
                                               :risk_evaluation {:low_risk low-tier
                                                                 :medium_risk medium-tier
                                                                 :high_risk high-tier}
                                               :custom_prompt (when (seq trimmed-prompt) trimmed-prompt)}]
                                  (if (= :edit form-type)
                                    (rf/dispatch [:ai-session-analyzer/update-rule @(:name state) payload])
                                    (rf/dispatch [:ai-session-analyzer/create-rule payload])))))}

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
                          :disabled (or @rule-loading? require-rule-missing?)
                          :type "submit"}
               "Save"]]]]

           ;; Free-plan upsell pinned below the header, non-dismissible.
           (when free-license?
             [:> Box {:class "mx-7 mt-4"}
              [enterprise-banner/main
               {:primary {:label "Talk to Sales"
                          :on-click promotion/request-demo}}]])]

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

          [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Heading {:as "h3" :size "4"} "Custom analysis prompt"]
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "Tell the model what to look for. Hoop appends a system prompt so the model always returns a low/medium/high grade."]]
            [:> Box {:grid-column "span 5 / span 5" :class "space-y-radix-3"}
             [forms/textarea
              {:label "Your prompt (Optional)"
               :placeholder "e.g. Treat any query that touches the payments schema as high risk."
               :rows 6
               :not-margin-bottom? true
               :value @(:custom-prompt state)
               :on-change #(reset! (:custom-prompt state) (-> % .-target .-value))}]
             [:> Callout.Root {:size "1" :color "blue" :variant "soft"}
              [:> Callout.Icon
               [:> Info {:size 16}]]
              [:> Callout.Text
               "Hoop prepends a fixed system prompt before your instructions so the analyzer always returns a structured low/medium/high grade. This is what keeps the actions above reliable."]]
             [system-prompt-preview]]]

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
                               :high (:high-risk-action state))
                :rule-name-atom (case (:key level)
                                  :low (:low-risk-rule state)
                                  :medium (:medium-risk-rule state)
                                  :high (:high-risk-rule state))
                :access-request-rules access-rules}])]]]]))))

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
          ;; rule-form derives its local state from rule-data once, on mount.
          ;; Keying by the loaded data forces a remount when it settles
          ;; (edit fetch or activation-journey template seed), so the form
          ;; never keeps state from a stale first render.
          ^{:key (str (name form-type) "-" (hash (:data @active-rule)))}
          [rule-form form-type
           (if (= :success (:status @active-rule))
             (:data @active-rule)
             {})
           scroll-pos])

        (finally
          (.removeEventListener js/window "scroll" handle-scroll)
          (rf/dispatch [:ai-session-analyzer/clear-active-rule]))))))
