(ns webapp.features.ai-session-analyzer.views.session-analysis
  (:require
   ["@radix-ui/react-accordion" :as Accordion]
   ["@radix-ui/themes" :refer [Badge Box Flex Text]]
   ["lucide-react" :refer [AlertTriangle ChevronDown]]
   [clojure.string :as cs]
   [webapp.formatters :as formatters]))

(def ^:private risk-badge-color
  {"low"    "green"
   "medium" "orange"
   "high"   "red"})

(def ^:private action-label
  "Sidecar analyzer actions, as the rule form names them."
  {"allow"            "Allow"
   "warn"             "Warn"
   "block"            "Block"
   "defer"            "Defer to Rego"
   "require_approval" "Hold for approval"
   "require_review"   "Hold for approval"
   "ai-handoff"       "Hand off to AI agent"})

(def ^:private approver-label
  {"ai_agent" "AI agent"
   "human"    "Human"})

(def ^:private decision-color
  {"approved" "green"
   "rejected" "red"})

(defn- risk-badge [risk-level]
  [:> Badge {:variant "soft" :color (get risk-badge-color risk-level "gray")}
   (str (cs/upper-case (or risk-level "")) " RISK")])

(defn- decision-line
  "One step of a decision: a label, its values and the agent's reason."
  [label values reason]
  [:> Box
   (into [:> Flex {:align "center" :gap "2" :wrap "wrap"}
          [:> Text {:size "1" :class "text-[--gray-11]"} label]]
         values)
   (when-not (cs/blank? reason)
     [:> Text {:as "p" :size "1" :class "text-[--gray-11] mt-1 whitespace-pre-wrap"} reason])])

(def ^:private ai-rule-kind
  "AI rule kinds (`metadata.sidecar.ai_decisions[].ai_rules[].kind`)."
  {"guardrail" {:label "Guardrail" :color "blue"}
   "mask"      {:label "Data masking" :color "purple"}})

(def ^:private max-summary-chars 160)

(defn- as-text
  "A definition value as one line: a list joined by commas, anything else as
  its string. Created definitions come from the model, so no shape is assumed."
  [v]
  (if (sequential? v)
    (cs/join ", " (map str v))
    (str v)))

(defn- shorten [s]
  (if (> (count s) max-summary-chars)
    (str (subs s 0 (dec max-summary-chars)) "\u2026")
    s))

(defn- present? [v]
  (not (cs/blank? (as-text v))))

(defn- guardrail-summary
  [{:keys [type words pattern_regex operations tables access entities]}]
  (let [scope (when (and (present? operations) (not= type "operation"))
                (str " on " (as-text operations)))
        [label value] (case type
                        "deny_words_list" ["Deny words" words]
                        "pattern_match"   ["Pattern" pattern_regex]
                        "table"           [(str "Tables" (when (present? access) (str " (" (as-text access) ")")))
                                           tables]
                        "pii"             ["PII" entities]
                        "operation"       ["Operations" operations]
                        [(str "Guardrail of type " (if (present? type) (as-text type) "unknown")) nil])]
    (str label scope (when (present? value) (str ": " (as-text value))))))

(defn- mask-summary [{:keys [strategy entities columns]}]
  (let [targets (remove nil? [(when (present? entities) (str "entities " (as-text entities)))
                              (when (present? columns) (str "columns " (as-text columns)))])]
    (str (cs/capitalize (if (present? strategy) (as-text strategy) "redact"))
         (when (seq targets) (str " " (cs/join "; " targets))))))

(defn- definition-summary
  "A created rule's definition in one short line."
  [kind definition]
  (cond
    (not (map? definition)) "definition not recorded"
    (= kind "guardrail") (shorten (guardrail-summary definition))
    :else (shorten (mask-summary definition))))

(defn- definition-json [definition]
  ;; js->clj keywordized the keys; keep a key's namespace part ("a/b").
  (js/JSON.stringify (clj->js definition :keyword-fn #(subs (str %) 1)) nil 2))

(defn- definition-block
  "The full definition of a created rule, collapsed."
  [definition]
  (when (map? definition)
    [:details {:class "mt-1"}
     [:summary {:class "cursor-pointer w-fit text-xs text-[--gray-11]"} "Definition"]
     [:pre {:tab-index 0
            :class "mt-1 max-h-64 overflow-auto rounded bg-[--gray-3] p-2 text-xs font-mono text-[--gray-12] whitespace-pre-wrap break-all"}
      (definition-json definition)]]))

(defn- kind-badge [kind]
  (let [{:keys [label color]} (get ai-rule-kind kind {:label kind :color "gray"})]
    [:> Badge {:variant "soft" :color color} label]))

(defn- ai-rule-item [{:keys [kind rule definition reason matched]}]
  [:li {:class "py-1"}
   [:> Flex {:align "center" :gap "2" :wrap "wrap"}
    [kind-badge kind]
    [:> Text {:size "1" :class "text-[--gray-11]"} (if (cs/blank? rule) "Created:" "Applied:")]
    [:> Text {:size "1" :weight "medium" :class "text-[--gray-12] break-all"}
     (if (cs/blank? rule) (definition-summary kind definition) rule)]
    (when (true? matched)
      [:> Badge {:variant "soft" :color "red"} "Matched"])]
   (when-not (cs/blank? reason)
     [:> Text {:as "p" :size "1" :class "text-[--gray-11] mt-1 whitespace-pre-wrap"} reason])
   (when (cs/blank? rule)
     [definition-block definition])])

(defn- ai-rule-invalid-item [{:keys [kind definition reason error]}]
  [:li {:class "py-1"}
   [:> Flex {:align "center" :gap "2" :wrap "wrap"}
    [:> AlertTriangle {:size 14 :aria-hidden true :className "text-[--amber-11] shrink-0"}]
    [kind-badge kind]
    [:> Text {:size "1" :class "text-[--amber-11]"} "Invalid, not applied:"]
    [:> Text {:size "1" :weight "medium" :class "text-[--gray-12] break-all"}
     (definition-summary kind definition)]]
   (when-not (cs/blank? error)
     [:> Text {:as "p" :size "1" :class "text-[--amber-11] mt-1 whitespace-pre-wrap"} error])
   (when-not (cs/blank? reason)
     [:> Text {:as "p" :size "1" :class "text-[--gray-11] mt-1 whitespace-pre-wrap"} reason])
   [definition-block definition]])

(defn- ai-rules-section
  "The rules the AI applied or created for one decision. `ai-rules` nil means
  no rule turn ran; an empty list means it ran and applied none."
  [ai-rules ai-rule-invalid ai-rules-error]
  (when (or (some? ai-rules) (seq ai-rule-invalid) (not (cs/blank? ai-rules-error)))
    [:> Box
     (when (or (some? ai-rules) (seq ai-rule-invalid))
       [:> Text {:size "1" :class "text-[--gray-11]"} "AI rules:"])
     (if (seq ai-rules)
       [:ul {:aria-label "AI rules" :class "mt-1"}
        (map-indexed (fn [idx r] ^{:key idx} [ai-rule-item r]) ai-rules)]
       (when (some? ai-rules)
         [:> Text {:as "p" :size "1" :class "text-[--gray-11] mt-1"} "None applied."]))
     (when (seq ai-rule-invalid)
       [:ul {:aria-label "Invalid AI rules" :class "mt-1"}
        (map-indexed (fn [idx r] ^{:key idx} [ai-rule-invalid-item r]) ai-rule-invalid)])
     (when-not (cs/blank? ai-rules-error)
       [:> Text {:as "p" :size "1" :class "text-[--gray-11] whitespace-pre-wrap"}
        (str "AI rules not applied: " ai-rules-error)])]))

(defn- ai-decision-row
  [{:keys [timestamp risk_level risk_action handoff_decision handoff_reason
           approver approval_decision approval_reason_code approval_reason review_id
           ai_rules ai_rule_invalid ai_rules_error]}]
  [:li {:class "py-3 border-t border-[--gray-3] first:border-t-0"}
   [:> Flex {:direction "column" :gap "2"}
    [:> Flex {:align "center" :gap "3" :wrap "wrap"}
     [risk-badge risk_level]
     [:> Text {:size "1" :weight "medium" :class "text-[--gray-12]"}
      (get action-label risk_action risk_action)]
     (when timestamp
       [:> Text {:size "1" :class "text-[--gray-11]"}
        (formatters/time-parsed->full-date timestamp)])]
    (when handoff_decision
      [decision-line "Hand-off decision:"
       [[:> Text {:size "1" :weight "medium" :class "text-[--gray-12]"}
         (get action-label handoff_decision handoff_decision)]]
       handoff_reason])
    (when (or approver approval_decision)
      [decision-line "Approver:"
       [(when approver
          [:> Text {:size "1" :weight "medium" :class "text-[--gray-12]"}
           (get approver-label approver approver)])
        (when approval_decision
          [:> Badge {:variant "soft" :color (get decision-color approval_decision "gray")}
           (cs/upper-case approval_decision)])
        (when-not (cs/blank? approval_reason_code)
          [:> Text {:size "1" :class "text-[--gray-11] font-mono"} approval_reason_code])]
       approval_reason])
    [ai-rules-section ai_rules ai_rule_invalid ai_rules_error]
    (when review_id
      [:a {:href (str "/approvals/" (js/encodeURIComponent review_id))
           :class "text-xs text-blue-600 underline font-mono w-fit"}
       (str "Approval " review_id)])]])

(defn ai-decisions
  "The sidecar analyzer's decision for each statement of a sidecar session
  (`metadata.sidecar.ai_decisions`). Renders nothing when there is none."
  [{:keys [decisions dropped]}]
  (when (seq decisions)
    [:> (.-Root Accordion) {:type "single"
                            :collapsible true
                            :class "w-full p-3 bg-[--gray-1] rounded-md border border-[--gray-3]"}
     [:> (.-Item Accordion) {:value "ai-decisions"
                             :className "border-none"}
      [:> (.-Header Accordion)
       [:> (.-Trigger Accordion) {:className "group flex w-full items-center justify-between gap-3 text-left focus:outline-none focus-visible:ring focus-visible:ring-gray-500 focus-visible:ring-opacity-75"}
        [:> Flex {:align "center" :gap "2"}
         [:img {:src "/images/ai-session-analyzer-logo.svg"
                :class "w-5 h-5 shrink-0"
                :alt ""}]
         [:> Text {:size "2" :weight "bold"} "AI decisions"]]
        [:> Flex {:align "center" :gap "3"}
         [:> Badge {:variant "soft" :color "gray"}
          (str (+ (count decisions) (or dropped 0)))]
         [:> ChevronDown {:size 16
                          :aria-hidden true
                          :className "text-gray-10 transition-transform group-data-[state=open]:rotate-180 shrink-0"}]]]]
      [:> (.-Content Accordion) {:className "mt-3"}
       [:ul {:aria-label "AI decisions"}
        (map-indexed
         (fn [idx decision]
           ^{:key idx}
           [ai-decision-row decision])
         decisions)]
       (when (and dropped (pos? dropped))
         [:> Text {:as "p" :size "1" :class "text-[--gray-11] pt-3 border-t border-[--gray-3]"}
          (str dropped " more decisions are not shown.")])]]]))

(defn- render-step [idx {:keys [type thinking tool_name tool_input tool_output is_error]}]
  ;; `type` is an open string server-side: render nothing (not an empty padded
  ;; row) for values this view does not know about.
  (when-let [body (case type
                    "thinking"
                    [:> Text {:size "1" :class "italic text-[--gray-11]"} thinking]

                    "tool_call"
                    [:> Text {:size "1" :class "font-mono text-[--gray-12]"}
                     (str "\u25B8 " tool_name "(" tool_input ")")]

                    "tool_result"
                    [:> Box {:class "max-h-64 overflow-y-auto"}
                     [:> Text {:size "1" :class (str "font-mono whitespace-pre-wrap "
                                                     (if is_error "text-[--red-11]" "text-[--gray-11]"))}
                      tool_output]]

                    nil)]
    ^{:key idx}
    [:> Box {:class "py-1"} body]))

(defn main [{:keys [ai-analysis]}]
  (when (and ai-analysis (seq ai-analysis))
    (let [{:keys [risk_level title explanation action summary steps]} ai-analysis
          badge-color (get risk-badge-color risk_level "gray")]
      [:> (.-Root Accordion) {:type "single"
                              :collapsible true
                              :class "w-full p-3 bg-[--gray-1] rounded-md border border-[--gray-3]"}
       [:> (.-Item Accordion) {:value "ai-session-analysis"
                               :className "border-none"}
        [:> (.-Header Accordion)
         [:> (.-Trigger Accordion) {:className "group flex w-full items-center justify-between gap-3 text-left focus:outline-none focus-visible:ring focus-visible:ring-gray-500 focus-visible:ring-opacity-75"}
          [:> Flex {:align "center" :gap "2"}
           [:img {:src "/images/ai-session-analyzer-logo.svg"
                  :class "w-5 h-5 shrink-0"
                  :alt "AI Session Analyzer"}]
           [:> Text {:size "2" :weight "bold"} "AI Session Analyzer"]]
          [:> Flex {:align "center" :gap "3"}
           [:> Flex {:align "center" :gap "2"
                     :class "group-data-[state=open]:hidden"}
            [:> Text {:size "2" :weight "medium" :class "text-[--gray-12]"} title]
            [:> Badge {:variant "soft" :color badge-color}
             (str (cs/upper-case risk_level) " RISK")]]
           [:> ChevronDown {:size 16
                            :className "text-gray-10 transition-transform group-data-[state=open]:rotate-180 shrink-0"}]]]]
        [:> (.-Content Accordion) {:className "mt-3"}
         [:> Flex {:direction "column" :gap "4"}
          [:> Flex {:align "center" :gap "4"}
           [:> Text {:size "2" :weight "medium" :class "text-[--gray-12]"} title]
           [:> Badge {:variant "soft" :color badge-color}
            (str (cs/upper-case risk_level) " RISK")]]
          [:> Text {:size "1" :class "text-[--gray-12]"} explanation]

          (when (and summary (seq summary))
            [:> Box {:class "pt-3 border-t border-[--gray-3]"}
             [:> Text {:size "1" :weight "medium" :class "text-[--gray-12]"} "Summary"]
             [:> Text {:as "p" :size "1" :class "text-[--gray-11] mt-1"} summary]])

          (when (seq steps)
            [:> Box {:class "pt-3 border-t border-[--gray-3]"}
             [:> Text {:size "1" :weight "medium" :class "text-[--gray-12]"} "Investigation trace"]
             [:> Flex {:direction "column" :gap "1" :class "mt-2"}
              (map-indexed render-step steps)]])

          (when action
            [:> Box {:class "pt-3 border-t border-[--gray-3]"}
             (case action
               "block_execution" [:> Badge {:color "red"} "ACTION BLOCKED"]
               "allow_execution" [:> Badge {:color "green"} "ACTION ALLOWED"]
               nil)])]]]])))
