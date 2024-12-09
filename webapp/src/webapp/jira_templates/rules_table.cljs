(ns webapp.jira-templates.rules-table
  (:require
   ["@radix-ui/themes" :refer [Box Flex Table Text Link]]
   [webapp.components.forms :as forms]
   [webapp.jira-templates.rule-buttons :as rule-buttons]))

(def type-options
  [{:value "hoop.dev" :text "hoop.dev"}
   {:value "custom" :text "custom"}])

(def hoop-value-options
  [{:value "session_id" :text "Session ID"}
   {:value "database_name" :text "Database name"}
   {:value "timestamp" :text "Timestamp"}
   {:value "user" :text "User"}])

(defn- rule-details [rule state idx on-rule-field-change]
  (when-not (empty? (:type rule))  ; Só mostra se type estiver selecionado
    (if (= "hoop.dev" (:type rule))
      [forms/select
       {:size "2"
        :variant "ghost"
        :not-margin-bottom? true
        :on-change #(on-rule-field-change state idx :value %)
        :selected (:value rule)
        :full-width? true
        :options hoop-value-options}]
      [forms/input
       {:size "2"
        :placeholder "Custom value"
        :name "value"
        :not-margin-bottom? true
        :value (:value rule)
        :on-change #(on-rule-field-change state idx :value (-> % .-target .-value))}])))

(defn- jira-field-input [rule state idx on-rule-field-change]
  (when-not (empty? (:type rule))  ; Só mostra se type estiver selecionado
    [forms/input
     {:size "2"
      :placeholder "Issue field"
      :value (:jira_field rule)
      :not-margin-bottom? true
      :on-change #(on-rule-field-change state idx :jira_field (-> % .-target .-value))}]))

(defn- details-input [rule state idx on-rule-field-change]
  (when-not (empty? (:type rule))  ; Só mostra se type estiver selecionado
    [forms/input
     {:size "2"
      :placeholder "Field description"
      :value (:description rule)
      :not-margin-bottom? true
      :on-change #(on-rule-field-change state idx :description (-> % .-target .-value))}]))

(defn main [{:keys [state
                    select-state
                    on-rule-field-change
                    on-rule-select
                    on-toggle-select
                    on-toggle-all
                    on-rules-delete
                    on-rule-add]}]
  [:> Box {:class "space-y-radix-5"}
   [:> Text {:size "3" :weight "bold" :class "text-[--gray-12]"}
    "Integration details"]
   [:> Table.Root {:size "2" :variant "surface"}
    [:> Table.Header
     [:> Table.Row {:align "center"}
      (when @select-state
        [:> Table.ColumnHeaderCell ""])
      [:> Table.ColumnHeaderCell "Type"]
      [:> Table.ColumnHeaderCell "Value"]
      [:> Table.ColumnHeaderCell "Jira Field"]
      [:> Table.ColumnHeaderCell "Details (Optional)"]]]

    [:> Table.Body
     (doall
      (for [[idx rule] (map-indexed vector @state)]
        ^{:key idx}
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
            :on-change (fn [value]
                        ;; Limpa os outros campos quando muda o type
                         (on-rule-field-change state idx :type value)
                         (on-rule-field-change state idx :value "")
                         (on-rule-field-change state idx :jira_field "")
                         (on-rule-field-change state idx :description ""))
            :selected (:type rule)
            :full-width? true
            :options type-options}]]

         [:> Table.Cell {:p "4"}
          [rule-details rule state idx on-rule-field-change]]

         [:> Table.Cell {:p "4"}
          [jira-field-input rule state idx on-rule-field-change]]

         [:> Table.Cell {:p "4"}
          [details-input rule state idx on-rule-field-change]]]))]]

   [rule-buttons/main
    {:on-rule-add #(on-rule-add state)
     :on-toggle-select #(on-toggle-select select-state)
     :select-state select-state
     :selected? (every? :selected @state)
     :on-toggle-all #(on-toggle-all state)
     :on-rules-delete #(on-rules-delete state)}]])
