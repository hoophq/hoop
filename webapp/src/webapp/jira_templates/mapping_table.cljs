(ns webapp.jira-templates.mapping-table
  (:require
   ["@radix-ui/themes" :refer [Box Table Text Strong]]
   [webapp.components.forms :as forms]
   [webapp.jira-templates.rule-buttons :as rule-buttons]))

(def type-options
  [{:value "preset" :text "Preset"}
   {:value "custom" :text "Custom"}])

(def hoop-value-options
  [{:value "session.id" :text "Session ID"}
   {:value "session.user_email" :text "User email"}
   {:value "session.user_id" :text "User ID"}
   {:value "session.user_name" :text "User name"}
   {:value "session.type" :text "Connection type"}
   {:value "session.connection" :text "Connection name"}
   {:value "session.status" :text "Session status"}
   {:value "session.start_date" :text "Session start date"}
   {:value "session.verb" :text "Session type"}])

(defn- value-field [rule state idx on-rule-field-change]
  (when-not (empty? (:type rule))
    (if (= "preset" (:type rule))
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
  (when-not (empty? (:type rule))
    [forms/input
     {:size "2"
      :placeholder "Issue field"
      :value (:jira_field rule)
      :not-margin-bottom? true
      :on-change #(on-rule-field-change state idx :jira_field (-> % .-target .-value))}]))

(defn- details-input [rule state idx on-rule-field-change]
  (when-not (empty? (:type rule))
    [forms/input
     {:size "2"
      :placeholder "Field description"
      :value (:description rule)
      :not-margin-bottom? true
      :on-change #(on-rule-field-change state idx :description (-> % .-target .-value))}]))

(defn main [{:keys [state
                    select-state
                    on-mapping-field-change
                    on-mapping-select
                    on-toggle-mapping-select
                    on-toggle-all-mapping
                    on-mapping-delete
                    on-mapping-add]}]
  [:> Box {:class "space-y-radix-5"}
   [:> Box
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
                      :on-change #(on-mapping-select state idx)}]])

          [:> Table.RowHeaderCell {:p "4" :width "160px"}
           [forms/select
            {:size "2"
             :name "type"
             :variant "ghost"
             :not-margin-bottom? true
             :on-change (fn [value]
                          (on-mapping-field-change state idx :type value)
                          (on-mapping-field-change state idx :value "")
                          (on-mapping-field-change state idx :jira_field "")
                          (on-mapping-field-change state idx :description ""))
             :selected (:type rule)
             :full-width? true
             :options type-options}]]

          [:> Table.Cell {:p "4"}
           [value-field rule state idx on-mapping-field-change]]

          [:> Table.Cell {:p "4"}
           [jira-field-input rule state idx on-mapping-field-change]]

          [:> Table.Cell {:p "4"}
           [details-input rule state idx on-mapping-field-change]]]))]]

    [:> Text {:as "p" :size "2" :mt "1" :class "text-[--gray-10]"}
     [:> Strong
      "Preset: "]
     "Relates hoop.dev fields with Jira fields. "
     [:> Strong
      "Custom: "]
     "Append a custom key-value relation to Jira cards."]]

   [rule-buttons/main
    {:on-rule-add #(on-mapping-add state)
     :on-toggle-select #(on-toggle-mapping-select select-state)
     :select-state select-state
     :selected? (every? :selected @state)
     :on-toggle-all #(on-toggle-all-mapping state)
     :on-rules-delete #(on-mapping-delete state)}]])
