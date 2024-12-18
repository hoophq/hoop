(ns webapp.jira-templates.cmdb-table
  (:require
   ["@radix-ui/themes" :refer [Box Table Text]]
   [webapp.components.forms :as forms]
   [webapp.jira-templates.rule-buttons :as rule-buttons]))

(def required-options
  [{:value "true" :text "Yes"}
   {:value "false" :text "No"}])

(defn main [{:keys [state
                    select-state
                    on-cmdb-field-change
                    on-cmdb-select
                    on-toggle-cmdb-select
                    on-toggle-all-cmdb
                    on-cmdb-delete
                    on-cmdb-add]}]
  [:> Box {:class "space-y-radix-5"}
   [:> Table.Root {:size "2" :variant "surface"}
    [:> Table.Header
     [:> Table.Row {:align "center"}
      (when @select-state
        [:> Table.ColumnHeaderCell ""])
      [:> Table.ColumnHeaderCell "Label"]
      [:> Table.ColumnHeaderCell "Jira Field"]
      [:> Table.ColumnHeaderCell "Value"]
      [:> Table.ColumnHeaderCell "Object Type"]
      [:> Table.ColumnHeaderCell "Description (Optional)"]
      [:> Table.ColumnHeaderCell "Required"]]]

    [:> Table.Body
     (doall
      (for [[idx cmdb] (map-indexed vector @state)]
        ^{:key idx}
        [:> Table.Row {:align "center"}
         (when @select-state
           [:> Table.RowHeaderCell {:p "2" :width "20px"}
            [:input {:type "checkbox"
                     :checked (:selected cmdb)
                     :on-change #(on-cmdb-select state idx)}]])

         [:> Table.Cell {:p "4"}
          [forms/input
           {:size "2"
            :placeholder "e.g. Employee ID"
            :value (:label cmdb)
            :not-margin-bottom? true
            :on-change #(on-cmdb-field-change state idx :label (-> % .-target .-value))}]]

         [:> Table.Cell {:p "4"}
          [forms/input
           {:size "2"
            :placeholder "e.g. customfield_0410"
            :value (:jira_field cmdb)
            :not-margin-bottom? true
            :on-change #(on-cmdb-field-change state idx :jira_field (-> % .-target .-value))}]]

         [:> Table.Cell {:p "4"}
          [forms/input
           {:size "2"
            :placeholder "e.g. value_123"
            :value (:value cmdb)
            :not-margin-bottom? true
            :on-change #(on-cmdb-field-change state idx :value (-> % .-target .-value))}]]

         [:> Table.Cell {:p "4"}
          [forms/input
           {:size "2"
            :placeholder "e.g. product"
            :value (:jira_object_type cmdb)
            :not-margin-bottom? true
            :on-change #(on-cmdb-field-change state idx :jira_object_type (-> % .-target .-value))}]]

         [:> Table.Cell {:p "4"}
          [forms/input
           {:size "2"
            :placeholder "e.g. customfield_0410"
            :value (:description cmdb)
            :not-margin-bottom? true
            :on-change #(on-cmdb-field-change state idx :description (-> % .-target .-value))}]]

         [:> Table.Cell {:p "4" :align "center"}
          [forms/select
           {:size "2"
            :variant "ghost"
            :not-margin-bottom? true
            :on-change #(on-cmdb-field-change state idx :required (= "true" %))
            :selected (str (:required cmdb))
            :full-width? true
            :options required-options}]]]))]]

   [rule-buttons/main
    {:on-rule-add #(on-cmdb-add state)
     :on-toggle-select #(on-toggle-cmdb-select select-state)
     :select-state select-state
     :selected? (every? :selected @state)
     :on-toggle-all #(on-toggle-all-cmdb state)
     :on-rules-delete #(on-cmdb-delete state)}]])
