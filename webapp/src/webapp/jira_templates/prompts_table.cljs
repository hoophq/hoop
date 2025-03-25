(ns webapp.jira-templates.prompts-table
  (:require
   ["@radix-ui/themes" :refer [Box Table]]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multi-select]
   [webapp.jira-templates.rule-buttons :as rule-buttons]))

(def required-options
  [{:value "true" :text "Yes"}
   {:value "false" :text "No"}])

(def field-type-options
  [{:value "text" :text "Text"}
   {:value "datetime-local" :text "Date"}
   {:value "select" :text "Select"}])

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
      [:> Table.ColumnHeaderCell "Type"]
      [:> Table.ColumnHeaderCell "Description (Optional)"]
      [:> Table.ColumnHeaderCell "Required"]]]

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

         [:> Table.Cell {:key (str idx "-label-" (:timestamp prompt)) :p "4"}
          [forms/input
           {:size "2"
            :placeholder "e.g. Employee ID"
            :value (:label prompt)
            :not-margin-bottom? true
            :on-change #(on-prompt-field-change state idx :label (-> % .-target .-value))}]]

         [:> Table.Cell {:key (str idx "-jira-field-" (:timestamp prompt)) :p "4"}
          [forms/input
           {:size "2"
            :placeholder "e.g. customfield_0410"
            :value (:jira_field prompt)
            :not-margin-bottom? true
            :on-change #(on-prompt-field-change state idx :jira_field (-> % .-target .-value))}]]

         [:> Table.Cell (merge
                         {:p "4"
                          :align "center"}
                         (when (= (:field_type prompt) "select")
                           {:width "256px"}))

          [:> Box {:class "space-y-radix-2"}
           [forms/select
            {:size "2"
             :variant "ghost"
             :not-margin-bottom? true
             :on-change #(on-prompt-field-change state idx :field_type %)
             :selected (str (:field_type prompt))
             :full-width? true
             :options field-type-options}]
           (when (= (:field_type prompt) "select")
             [multi-select/text-input
              {:value (:field_options prompt)
               :input-value (:select-input-value prompt)
               :on-change (fn [value]
                            (on-prompt-field-change state idx :field_options value))
               :on-input-change (fn [value]
                                  (on-prompt-field-change state idx :select-input-value value))
               :id (str "jira-prompt-select-" idx)
               :name (str "jira-prompt-select-" idx)}])]]

         [:> Table.Cell {:key (str idx "-description-" (:timestamp prompt)) :p "4"}
          [forms/input
           {:size "2"
            :placeholder "e.g. customfield_0410"
            :value (:description prompt)
            :not-margin-bottom? true
            :on-change #(on-prompt-field-change state idx :description (-> % .-target .-value))}]]

         [:> Table.Cell {:p "4" :align "center"}
          [forms/select
           {:size "2"
            :variant "ghost"
            :not-margin-bottom? true
            :on-change #(on-prompt-field-change state idx :required (= "true" %))
            :selected (str (:required prompt))
            :full-width? true
            :options required-options}]]]))]]

   [rule-buttons/main
    {:on-rule-add #(on-prompt-add state)
     :on-toggle-select #(on-toggle-prompt-select select-state)
     :select-state select-state
     :selected? (every? :selected @state)
     :on-toggle-all #(on-toggle-all-prompts state)
     :on-rules-delete #(on-prompt-delete state)}]])
