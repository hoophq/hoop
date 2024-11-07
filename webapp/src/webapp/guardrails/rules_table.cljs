(ns webapp.guardrails.rules-table
  (:require
   ["@radix-ui/themes" :refer [Box Flex Table Text]]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multi-select]
   [webapp.guardrails.rule-buttons :as rule-buttons]))

(defn- rule-details [rule state idx on-rule-field-change words-state on-word-change pattern-state on-pattern-change]
  (cond
    (and (= "custom-rule" (:rule rule))
         (= "patter-match" (:type rule)))
    [forms/input
     {:placeholder "Describe how this is used in your connections"
      :class "w-full"
      :on-change #(on-pattern-change pattern-state idx (-> % .-target .-value))
      :value (get @pattern-state idx "")}]

    (and (= "custom-rule" (:rule rule))
         (= "deny-word" (:type rule)))
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
      :id (str "deny-words-" idx)
      :name (str "deny-words-" idx)}]))

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
                    on-rules-delete
                    selected?]}]
  [:> Box {:class "space-y-radix-5"}
   [:> Text {:size "3" :weight "bold" :class "text-[--gray-12]"}
    title]
   [:> Table.Root {:size "2" :variant "surface"}
    [:> Table.Header
     [:> Table.Row
      (when @select-state
        [:> Table.ColumnHeaderCell ""])
      [:> Table.ColumnHeaderCell "Type"]
      [:> Table.ColumnHeaderCell "Rule"]
      [:> Table.ColumnHeaderCell "Details"]]]
    [:> Table.Body
     (doall
      (for [[idx rule] (map-indexed vector @state)]
        ^{:key (str (hash (str rule idx)))}
        [:> Table.Row
         (when @select-state
           [:> Table.RowHeaderCell {:width "20px"}
            [:input {:type "checkbox"
                     :checked (:selected rule)
                     :on-change #(on-rule-select state idx)}]])

         [:> Table.RowHeaderCell {:width "160px"}
          [forms/select
           {:size "2"
            :name "type"
            :variant "ghost"
            :not-margin-bottom? true
            :on-change #(on-rule-field-change state idx :type %)
            :selected (:type rule)
            :full-width? true
            :options [{:value "deny-word" :text "Deny Word"}
                      {:value "patter-match" :text "Pattern Match"}]}]]

         [:> Table.Cell {:width "220px"}
          [forms/select
           {:size "2"
            :name "rule"
            :variant "ghost"
            :not-margin-bottom? true
            :on-change #(on-rule-field-change state idx :rule %)
            :selected (:rule rule)
            :full-width? true
            :options [{:value "require-where" :text "Require WHERE clause"}
                      {:value "block-password" :text "Block Passwords"}
                      {:value "custom-rule" :text "Custom rule"}]}]]

         [:> Table.Cell
          [rule-details
           rule
           state
           idx
           on-rule-field-change
           words-state
           on-word-change
           pattern-state
           on-pattern-change]]]))]]

   ;; Botões de ação
   [rule-buttons/main
    {:on-rule-add #(on-rule-add state)
     :on-toggle-select #(on-toggle-select select-state)
     :select-state select-state
     :selected? selected?
     :on-toggle-all on-toggle-all
     :on-rules-delete #(on-rules-delete state)}]])
