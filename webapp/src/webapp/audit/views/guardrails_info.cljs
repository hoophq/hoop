(ns webapp.audit.views.guardrails-info
  (:require
   ["@radix-ui/react-accordion" :as Accordion]
   ["@radix-ui/themes" :refer [Badge Box Flex Text]]
   ["lucide-react" :refer [ChevronDown ShieldCheck]]
   [clojure.string :as cs]))

(def ^:private rule-type-config
  {"pattern_match"   {:label "PATTERN MATCH" :color "pink"}
   "deny_words_list" {:label "DENY WORD"     :color "red"}})

(defn- rule-type-badge [{:keys [rule]}]
  (let [{:keys [label color]} (get rule-type-config (:type rule)
                                   {:label (cs/upper-case (or (:type rule) "UNKNOWN"))
                                    :color "gray"})]
    [:> Badge {:variant "soft" :color color} label]))

(defn- direction-label [direction]
  (str (cs/capitalize (or direction "input")) " Rule:"))

(defn- blocked-message [rule-type direction matched-words]
  (let [verb (if (= direction "output") "Response" "Query")]
    (case rule-type
      "deny_words_list" (str verb " blocked — " (count matched-words) " forbidden "
                             (if (= 1 (count matched-words)) "keyword" "keywords") " detected")
      "pattern_match"   (str verb " blocked — pattern violation detected")
      (str verb " blocked"))))

(defn- word-chip [word]
  [:> Box {:class "p-2 bg-gray-4 text-xs font-mono" :style {:borderRadius "var(--Radius-2, 6px)"}}
   word])

(defn- matched-words-section [{:keys [rule matched_words]}]
  (when (seq matched_words)
    (if (= (:type rule) "pattern_match")
      [:> Flex {:direction "column" :gap "4"}
       [:> Text {:size "2" :weight "medium"} "Violation:"]
       [:> Flex {:gap "1" :wrap "wrap"}
        (for [word matched_words]
          ^{:key word} [word-chip word])]]
      [:> Flex {:gap "1" :wrap "wrap"}
       (for [word matched_words]
         ^{:key word} [word-chip word])])))

(defn- accordion-icon []
  [:> Box {:class "flex items-center justify-center shrink-0 text-white rounded-1 bg-[var(--blue-11)] w-6 h-6"}
   [:> ShieldCheck {:size 16}]])

(defn- chevron []
  [:> ChevronDown {:size 16
                   :className "text-gray-10 transition-transform group-data-[state=open]:rotate-180 shrink-0"}])

(defn- single-card [{:keys [rule_name rule direction matched_words] :as entry}]
  [:> (.-Root Accordion) {:type "single"
                          :collapsible true
                          :class "w-full p-3 bg-[--gray-1] rounded-md border border-[--gray-3]"}
   [:> (.-Item Accordion) {:value "guardrails" :className "border-none"}
    [:> (.-Header Accordion)
     [:> (.-Trigger Accordion) {:className "group flex w-full items-center justify-between gap-3 text-left focus:outline-none focus-visible:ring focus-visible:ring-gray-500 focus-visible:ring-opacity-75"}
      [:> Flex {:align "center" :gap "2"}
       [accordion-icon]
       [:> Text {:size "2" :weight "bold"} "Guardrails"]]
      [:> Flex {:align "center" :gap "3"}
       [:> Flex {:align "center" :gap "2"}
        [:> Text {:size "2" :weight "medium" :class "text-[--gray-12]"} rule_name]
        [rule-type-badge entry]]
       [chevron]]]]
    [:> (.-Content Accordion) {:className "pt-3 mt-3 border-t border-[--gray-3]"}
     [:> Flex {:direction "column" :gap "4"}
      [:> Text {:size "2" :weight "bold"} (direction-label direction)]
      [:> Text {:size "2"} (blocked-message (:type rule) direction matched_words)]
      [matched-words-section entry]]]]])

(defn- multi-entry [{:keys [rule_name rule direction matched_words] :as entry} first?]
  [:> Flex {:direction "column" :gap "4"
            :class (when-not first? "pt-4 mt-4 border-t border-[--gray-3]")}
   [:> Text {:size "2" :weight "bold"} (direction-label direction)]
   [:> Flex {:align "center" :gap "2"}
    [:> Text {:size "2" :weight "medium" :class "text-[--gray-12]"} rule_name]
    [rule-type-badge entry]]
   [:> Text {:size "2"} (blocked-message (:type rule) direction matched_words)]
   [matched-words-section entry]])

(defn- multi-card [guardrails-info]
  (let [total (count guardrails-info)]
    [:> (.-Root Accordion) {:type "single"
                            :collapsible true
                            :class "w-full p-3 bg-[--gray-1] rounded-md border border-[--gray-3]"}
     [:> (.-Item Accordion) {:value "guardrails" :className "border-none"}
      [:> (.-Header Accordion)
       [:> (.-Trigger Accordion) {:className "group flex w-full items-center justify-between gap-3 text-left focus:outline-none focus-visible:ring focus-visible:ring-gray-500 focus-visible:ring-opacity-75"}
        [:> Flex {:align "center" :gap "2"}
         [accordion-icon]
         [:> Text {:size "2" :weight "bold"} "Guardrails"]]
        [:> Flex {:align "center" :gap "2"}
         [:> Text {:size "2" :class "text-[--gray-11]"}
          [:span {:class "hidden group-data-[state=open]:inline"} "Hide details"]
          [:span {:class "inline group-data-[state=open]:hidden"} "Show details"]]
         [:> Badge {:variant "soft" :color "gray"} (str total)]
         [chevron]]]]
      [:> (.-Content Accordion) {:className "pt-3 mt-3 border-t border-[--gray-3]"}
       [:> Flex {:direction "column"}
        (map-indexed (fn [idx entry]
                       ^{:key idx} [multi-entry entry (= idx 0)])
                     guardrails-info)]]]]))

(defn main [{:keys [guardrails-info]}]
  (when (seq guardrails-info)
    (if (= 1 (count guardrails-info))
      [single-card (first guardrails-info)]
      [multi-card guardrails-info])))
