(ns webapp.features.rulepacks.views.list
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Flex Heading Text TextField]]
   ["lucide-react" :refer [ArrowRight EyeOff Search Shield Sparkles]]
   [clojure.string :as str]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]))

(def ^:private search-debounce-ms 300)

(defn- feature-badge [icon label]
  [:> Badge {:variant "soft" :color "gray" :size "1"
             :class "gap-1 px-1.5 py-0.5"}
   [:> icon {:size 12}]
   label])

(defn- rulepack-row [{:keys [id display_name description
                              data_masking_rules guardrail_rules ai_session_analyzer_rules]
                      :as _rulepack}
                     first? last?]
  (let [has-data-masking? (seq data_masking_rules)
        has-guardrails? (seq guardrail_rules)
        has-ai-analyzer? (seq ai_session_analyzer_rules)
        any-badge? (or has-data-masking? has-guardrails? has-ai-analyzer?)]
    [:> Box {:class (str "bg-white overflow-hidden "
                         (when (not first?) "border-t border-[--gray-a6] ")
                         (when first? "first:rounded-t-6 ")
                         (when last? "last:rounded-b-6 "))}
     [:> Flex {:gap "5" :align "center" :p "5"}
      [:> Flex {:direction "column" :class "flex-1 min-w-0"}
       [:> Heading {:as "h3" :size "5" :weight "bold"
                    :class "text-[--gray-12] truncate"}
        display_name]
       (when (seq description)
         [:> Text {:size "3" :class "text-[--gray-11]"}
          description])
       (when any-badge?
         [:> Flex {:gap "2" :pt "2"}
          (when has-data-masking?
            [feature-badge EyeOff "Data Masking"])
          (when has-guardrails?
            [feature-badge Shield "Guardrails"])
          (when has-ai-analyzer?
            [feature-badge Sparkles "AI Session Analyzer"])])]
      [:> Button {:variant "soft" :size "3"
                  :on-click #(rf/dispatch [:navigate :rulepack-detail {} :rulepack-id id])}
       "Configure"
       [:> ArrowRight {:size 18}]]]]))

(defn- empty-state [{:keys [searching?]}]
  [:> Box {:p "9" :class "text-center"}
   [:> Heading {:as "h3" :size "5" :weight "medium" :class "text-[--gray-12]"}
    (if searching? "No rulepacks match your search" "No rulepacks yet")]
   [:> Text {:size "3" :class "text-[--gray-11]"}
    (if searching?
      "Try a different search term."
      "Rulepacks bundle related rules and let you apply them to many connections at once.")]])

(defn main []
  (let [input (r/atom "")
        debounce-timer (r/atom nil)
        dispatch-search (fn [q]
                          (when @debounce-timer
                            (js/clearTimeout @debounce-timer))
                          (reset! debounce-timer
                                  (js/setTimeout
                                   #(do (reset! debounce-timer nil)
                                        (rf/dispatch [:rulepacks/list {:search q}]))
                                   search-debounce-ms)))]
    (rf/dispatch [:rulepacks/list])
    (fn []
      (let [rulepacks @(rf/subscribe [:rulepacks/list])
            status @(rf/subscribe [:rulepacks/list-status])
            active-search @(rf/subscribe [:rulepacks/list-search])
            loading? (= :loading status)
            searching? (or (seq @input) (seq active-search))]
        [:> Box {:class "bg-gray-1 p-radix-7 min-h-full h-max"}
         [:> Box {:pb "5" :pt "7"}
          [:> Heading {:as "h1" :size "8" :weight "bold" :class "text-[--gray-12]"}
           "Rulepacks"]
          [:> Text {:size "5" :class "text-[--gray-11]"}
           "Bundle configurations and apply them to many connections at once."]]

         [:> Box {:pb "4"}
          [:> TextField.Root {:value @input
                              :on-change (fn [e]
                                           (let [v (.. e -target -value)]
                                             (reset! input v)
                                             (dispatch-search v)))
                              :placeholder "Search rulepacks by name"
                              :size "3"
                              :class "max-w h-10"}
           [:> TextField.Slot
            [:> Search {:size 16}]]]]

         (cond
           loading?
           [:> Flex {:justify "center" :align "center" :py "9"}
            [loaders/simple-loader]]

           (empty? rulepacks)
           [:> Box {:class "bg-white border border-[--gray-a6] rounded-3xl"}
            [empty-state {:searching? (or searching?
                                          (and (not (str/blank? active-search))
                                               (empty? rulepacks)))}]]

           :else
           [:> Box {:class "bg-white border border-[--gray-a6] rounded-3xl overflow-hidden"}
            (let [n (count rulepacks)]
              (doall
               (map-indexed (fn [idx rp]
                              ^{:key (:id rp)}
                              [rulepack-row rp (zero? idx) (= idx (dec n))])
                            rulepacks)))])]))))
