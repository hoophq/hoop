(ns webapp.ai-data-masking.rules-table
  (:require
   ["@radix-ui/themes" :refer [Box Table Text Strong Badge]]
   [webapp.components.forms :as forms]
   [webapp.ai-data-masking.rule-buttons :as rule-buttons]
   [webapp.ai-data-masking.helpers :as helpers]))

(defn- type-field [rule state idx on-rule-field-change]
  [forms/select
   {:size "2"
    :variant "ghost"
    :not-margin-bottom? true
    :on-change (fn [value]
                 (on-rule-field-change state idx :type value)
                 (on-rule-field-change state idx :rule "")
                 (on-rule-field-change state idx :details ""))
    :selected (:type rule)
    :full-width? true
    :options helpers/rule-types}])

(defn- rule-field [rule state idx on-rule-field-change]
  (when-not (empty? (:type rule))
    (case (:type rule)
      "presets"
      [forms/select
       {:size "2"
        :variant "ghost"
        :not-margin-bottom? true
        :on-change #(on-rule-field-change state idx :rule %)
        :selected (:rule rule)
        :full-width? true
        :options (helpers/get-preset-options)}]

      "fields"
      [forms/select
       {:size "2"
        :variant "ghost"
        :not-margin-bottom? true
        :on-change #(on-rule-field-change state idx :rule %)
        :selected (:rule rule)
        :full-width? true
        :options (helpers/get-field-options)}]

      "custom"
      [forms/input
       {:size "2"
        :placeholder "Rule Name"
        :name "rule"
        :not-margin-bottom? true
        :value (:rule rule)
        :on-change #(let [raw-value (-> % .-target .-value)
                          normalized-value (helpers/normalize-entity-name raw-value)]
                      (on-rule-field-change state idx :rule normalized-value))}]

      nil)))

(defn- details-field [rule state idx on-rule-field-change]
  (when-not (empty? (:type rule))
    (case (:type rule)
      ("presets" "fields")
      (let [values (if (= "presets" (:type rule))
                     (helpers/get-preset-values (:rule rule))
                     [(:rule rule)])]
        [:> Box {:class "flex flex-wrap gap-1"}
         (for [value values]
           ^{:key value}
           [:> Badge {:size "2" :variant "solid" :color "gray"}
            value])])

      "custom"
      [forms/input
       {:size "2"
        :placeholder "\\b[A-Z]{2}[0-9]{3}\\b"
        :name "details"
        :not-margin-bottom? true
        :value (:details rule)
        :on-change #(on-rule-field-change state idx :details (-> % .-target .-value))}]

      nil)))

(defn main [{:keys [state
                    select-state
                    on-rule-field-change
                    on-rule-select
                    on-toggle-rules-select
                    on-toggle-all-rules
                    on-rules-delete
                    on-rule-add]}]
  [:> Box {:class "space-y-radix-5"}
   [:> Box
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
         ^{:key (:rule rule)}
         [:> Table.Row {:align "center"}
          (when @select-state
            [:> Table.RowHeaderCell {:p "2" :width "20px"}
             [:input {:type "checkbox"
                      :checked (:selected rule)
                      :on-change #(on-rule-select state idx)}]])

          [:> Table.RowHeaderCell {:p "4" :width "160px"}
           [type-field rule state idx on-rule-field-change]]

          [:> Table.Cell {:p "4"}
           [rule-field rule state idx on-rule-field-change]]

          [:> Table.Cell {:p "4"}
           [details-field rule state idx on-rule-field-change]]]))]]]

   [rule-buttons/main
    {:on-rule-add #(on-rule-add state)
     :on-toggle-select #(on-toggle-rules-select select-state)
     :select-state select-state
     :selected? (every? :selected @state)
     :on-toggle-all #(on-toggle-all-rules state)
     :on-rules-delete #(on-rules-delete state)}]])
