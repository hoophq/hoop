(ns webapp.jira-templates.prompts-table
  (:require
   ["@radix-ui/themes" :refer [Box Table Text]]
   [webapp.components.forms :as forms]
   [webapp.jira-templates.rule-buttons :as rule-buttons]))

(def required-options
  [{:value "true" :text "Yes"}
   {:value "false" :text "No"}])

(defn main [{:keys [state
                    select-state
                    on-prompt-field-change
                    on-prompt-select
                    on-toggle-prompt-select
                    on-toggle-all-prompts
                    on-prompt-delete
                    on-prompt-add]}]
  [:> Box {:class "space-y-radix-5"}
   [:> Table.Root {:size "2" :variant "surface"}
    [:> Table.Header
     [:> Table.Row {:align "center"}
      (when @select-state
        [:> Table.ColumnHeaderCell ""])
      [:> Table.ColumnHeaderCell "Label"]
      [:> Table.ColumnHeaderCell "Jira Field"]
      [:> Table.ColumnHeaderCell "Required"]
      [:> Table.ColumnHeaderCell "Details (Optional)"]]]

    [:> Table.Body
     (doall
      (for [[idx prompt] (map-indexed vector @state)]
        ^{:key idx}
        [:> Table.Row {:align "center"}
         (when @select-state
           [:> Table.RowHeaderCell {:p "2" :width "20px"}
            [:input {:type "checkbox"
                     :checked (:selected prompt)
                     :on-change #(on-prompt-select state idx)}]])

         [:> Table.Cell {:p "4"}
          [forms/input
           {:size "2"
            :placeholder "Label"
            :value (:label prompt)
            :not-margin-bottom? true
            :on-change #(on-prompt-field-change state idx :label (-> % .-target .-value))}]]

         [:> Table.Cell {:p "4"}
          [forms/input
           {:size "2"
            :placeholder "custom_0026"
            :value (:jira_field prompt)
            :not-margin-bottom? true
            :on-change #(on-prompt-field-change state idx :jira_field (-> % .-target .-value))}]]

         [:> Table.Cell {:p "4" :align "center"}
          [forms/select
           {:size "2"
            :variant "ghost"
            :not-margin-bottom? true
            :on-change #(on-prompt-field-change state idx :required (= "true" %))
            :selected (str (:required prompt))
            :full-width? true
            :options required-options}]]

         [:> Table.Cell {:p "4"}
          [forms/input
           {:size "2"
            :placeholder "Adicional information"
            :value (:description prompt)
            :not-margin-bottom? true
            :on-change #(on-prompt-field-change state idx :description (-> % .-target .-value))}]]]))]]

   [rule-buttons/main
    {:on-rule-add #(on-prompt-add state)
     :on-toggle-select #(on-toggle-prompt-select select-state)
     :select-state select-state
     :selected? (every? :selected @state)
     :on-toggle-all #(on-toggle-all-prompts state)
     :on-rules-delete #(on-prompt-delete state)}]])
