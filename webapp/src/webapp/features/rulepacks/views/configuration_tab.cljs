(ns webapp.features.rulepacks.views.configuration-tab
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading Text]]
   ["lucide-react" :refer [EyeOff Shield]]))

(defn- rule-row [{:keys [name description]} first? last?]
  [:> Box {:class (str "bg-white overflow-hidden "
                       (when (not first?) "border-t border-[--gray-a6] ")
                       (when first? "first:rounded-t-6 ")
                       (when last? "last:rounded-b-6 "))}
   [:> Flex {:direction "column" :p "5" :gap "1"}
    [:> Heading {:as "h4" :size "5" :weight "bold" :class "text-[--gray-12] truncate"}
     name]
    (when (seq description)
      [:> Text {:size "3" :class "text-[--gray-11]"}
       description])]])

(defn- section [{:keys [icon title description rules]}]
  (when (seq rules)
    [:> Flex {:direction "column" :gap "4" :class "w-full"}
     [:> Flex {:gap "3" :align "center" :class "w-full"}
      [:> Flex {:justify "center" :align "center"
                :class "bg-[--gray-a3] rounded-3 w-10 h-10 shrink-0"}
       [:> icon {:size 16}]]
      [:> Flex {:direction "column" :class "flex-1 min-w-0"}
       [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
        (str title " • " (count rules) " rule" (when (not= 1 (count rules)) "s"))]
       (when description
         [:> Text {:size "3" :class "text-[--gray-11]"}
          description])]]
     [:> Box {:class "bg-white border border-[--gray-a6] rounded-3xl overflow-hidden w-full"}
      (let [n (count rules)]
        (doall
         (map-indexed (fn [idx r]
                        ^{:key (or (:id r) (:name r))}
                        [rule-row r (zero? idx) (= idx (dec n))])
                      rules)))]]))

(defn main [{:keys [rulepack]}]
  (let [dm (or (:data_masking_rules rulepack) [])
        gr (or (:guardrail_rules rulepack) [])
        nothing? (and (empty? dm) (empty? gr))]
    [:> Flex {:direction "column" :gap "6" :class "w-full"}
     (when nothing?
       [:> Box {:p "7" :class "bg-white border border-[--gray-a6] rounded-3xl text-center w-full"}
        [:> Text {:size "3" :class "text-[--gray-11]"}
         "This rulepack has no rules configured."]])
     [section {:icon EyeOff
               :title "Data Masking"
               :description "Redact PII and secrets in command output before they reach the user."
               :rules dm}]
     [section {:icon Shield
               :title "Guardrails"
               :description "Block risky inputs and outputs before they reach the target system."
               :rules gr}]]))
