(ns webapp.features.ai-session-analyzer.views.session-analysis
  (:require
   ["@radix-ui/react-accordion" :as Accordion]
   ["@radix-ui/themes" :refer [Badge Box Flex Text]]
   ["lucide-react" :refer [ChevronDown]]
   [clojure.string :as cs]))

(def ^:private risk-badge-color
  {"low"    "green"
   "medium" "orange"
   "high"   "red"})

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
