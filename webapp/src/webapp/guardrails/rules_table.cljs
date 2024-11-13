(ns webapp.guardrails.rules-table
  (:require
   ["@radix-ui/themes" :refer [Box Flex Select Table Text Tooltip]]
   ["lucide-react" :refer [CircleHelp]]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multi-select]
   [webapp.guardrails.rule-buttons :as rule-buttons]))

(def rule-type-options
  [{:value "deny_words_list" :text "Deny Word"}
   {:value "pattern_match" :text "Pattern Match"}])

(def preset-values
  {"require-where-delete"
   {:type "pattern_match"
    :pattern_regex "(?i)DELETE\\s+FROM\\s+(\\w+\\.)*\\w+[^WHERE]*$"}

   "block-password"
   {:type "deny_words_list"
    :words ["password" "senha" "pass" "pwd"]}})

(defn- get-rule-options [type]
  (case type
    "pattern_match"
    [:<>
     [:> Select.Item {:value "require-where-delete"} "Require WHERE clause (DELETE)"]]

    "deny_words_list"
    [:> Select.Item {:value "block-password"} "Block Passwords"]

    [:<>]))

(defn- handle-type-change [state idx value on-rule-field-change]
  ;; When the type changes, we need to clear the rule field and other related fields
  (on-rule-field-change state idx :type value)
  (on-rule-field-change state idx :rule "")
  (on-rule-field-change state idx :pattern_regex "")
  (on-rule-field-change state idx :words []))

(defn- handle-preset-change [state idx value on-rule-field-change pattern-state words-state]
  (if (= value "custom-rule")
    ;; If selecting custom rule, just update the rule field
    (on-rule-field-change state idx :rule value)
    ;; If selecting a preset, apply the preset values
    (let [preset (get preset-values value)]
      (when preset
        (on-rule-field-change state idx :rule value)
        (case (:type preset)
          "pattern_match"
          (do
            (swap! pattern-state assoc idx (:pattern_regex preset))
            (on-rule-field-change state idx :pattern_regex (:pattern_regex preset)))

          "deny_words_list"
          (do
            (on-rule-field-change state idx :words (:words preset))
            (swap! words-state assoc idx ""))

          nil)))))

(defn- rule-details [rule state idx on-rule-field-change words-state on-word-change pattern-state on-pattern-change]
  (cond
    (= "pattern_match" (:type rule))
    [:> Flex {:align "center" :gap "2"}
     [forms/input
      {:placeholder "Describe how this is used in your connections"
       :full-width? true
       :size "3"
       :not-margin-bottom? true
       :on-change #(on-pattern-change pattern-state idx (-> % .-target .-value))
       :on-blur #(on-rule-field-change state idx :pattern_regex (get @pattern-state idx ""))
       :value (get @pattern-state idx "")}]
     [:> Tooltip {:content "Use regex Go syntax. Beta feature."}
      [:> CircleHelp {:size 16}]]]

    (= "deny_words_list" (:type rule))
    [multi-select/text-input
     {:value (if (empty? (:words rule))
               []
               (mapv #(into {} {"value" % "label" %}) (:words rule)))
      :input-value (get @words-state idx "")
      :on-change (fn [value]
                   (on-rule-field-change
                    state
                    idx
                    :words
                    (mapv #(get % "value") value)))
      :on-input-change #(on-word-change words-state idx %)
      :id (str "deny_words_list-" idx)
      :name (str "deny_words_list-" idx)}]))

(defn main [{:keys [title
                    state
                    select-state
                    words-state
                    pattern-state
                    on-word-change
                    on-pattern-change
                    on-rule-select
                    on-rule-field-change
                    on-rule-add
                    on-toggle-select
                    on-toggle-all
                    on-rules-delete]}]
  (let [selected? (every? :selected @state)]
    [:> Box {:class "space-y-radix-5"}
     [:> Text {:size "3" :weight "bold" :class "text-[--gray-12]"}
      title]
     [:> Table.Root {:size "2" :variant "surface"}
      [:> Table.Header
       [:> Table.Row {:align "center"}
        (when @select-state
          [:> Table.ColumnHeaderCell ""])
        [:> Table.ColumnHeaderCell "Type"]
        [:> Table.ColumnHeaderCell "Rule"]
        [:> Table.ColumnHeaderCell "Details"]]]
      [:> Table.Body
       (doall
        (for [[idx rule] (map-indexed vector @state)]
          ^{:key (str (hash (str rule idx)))}
          [:> Table.Row {:align "center"}
           (when @select-state
             [:> Table.RowHeaderCell {:p "2" :width "20px"}
              [:input {:type "checkbox"
                       :checked (:selected rule)
                       :on-change #(on-rule-select state idx)}]])

           [:> Table.RowHeaderCell {:p "4" :width "160px"}
            [forms/select
             {:size "2"
              :name "type"
              :variant "ghost"
              :not-margin-bottom? true
              :on-change #(handle-type-change state idx % on-rule-field-change)
              :selected (:type rule)
              :full-width? true
              :options rule-type-options}]]

           [:> Table.Cell {:p "4" :width "220px"}
            (when-not (empty? (:type rule))
              [:div {:class (str " text-sm w-full")}
               [:div {:class "flex items-center gap-2"}
                [:> Select.Root
                 {:size "2"
                  :value (:rule rule)
                  :on-value-change #(handle-preset-change state idx % on-rule-field-change pattern-state words-state)}
                 [:> Select.Trigger
                  {:placeholder "Select one"
                   :variant "ghost"
                   :class "w-full "}]
                 [:> Select.Content {:position "popper"}
                  [:> Select.Item {:value "custom-rule"
                                   :class (str "mt-2 flex items-center justify-center bg-blue-50 "
                                               "text-blue-600 rounded-md py-1 hover:bg-blue-100 "
                                               "cursor-pointer transition-colors")}
                   "Create custom rule"]
                  [:> Select.Group
                   [:> Select.Label "Presets"]
                   (get-rule-options (:type rule))]]]]])]

           [:> Table.Cell {:p "4"}
            (when-not (empty? (:rule rule))
              [rule-details
               rule
               state
               idx
               on-rule-field-change
               words-state
               on-word-change
               pattern-state
               on-pattern-change])]]))]]

   ;; Action buttons
     [rule-buttons/main
      {:on-rule-add #(on-rule-add state)
       :on-toggle-select #(on-toggle-select select-state)
       :select-state select-state
       :selected? selected?
       :on-toggle-all #(on-toggle-all state)
       :on-rules-delete #(on-rules-delete state)}]]))
