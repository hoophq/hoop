(ns webapp.jira-templates.mapping-table
  (:require
   ["@radix-ui/themes" :refer [Box Table Text Strong]]
   [webapp.components.forms :as forms]
   [webapp.jira-templates.rule-buttons :as rule-buttons]
   [clojure.string :as str]))

(def type-options
  [{:value "preset" :text "Preset"}
   {:value "custom" :text "Custom"}])

(def hoop-value-options
  [{:value "session.id" :text "Session ID"}
   {:value "session.user_email" :text "User email"}
   {:value "session.user_id" :text "User ID"}
   {:value "session.user_name" :text "User name"}
   {:value "session.type" :text "Resource role type"}
   {:value "session.connection" :text "Resource role name"}
   {:value "session.status" :text "Session status"}
   {:value "session.start_date" :text "Session start date"}
   {:value "session.verb" :text "Session type"}
   {:value "session.script" :text "Session Script"}])

(defn- is-connection-tag? [rule]
  (and (:value rule)
       (str/starts-with? (:value rule) "session.connection_tags.")))

(defn- is-not-connection-tag? [rule]
  (not (is-connection-tag? rule)))

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
        :placeholder "e.g. product"
        :name "value"
        :not-margin-bottom? true
        :value (:value rule)
        :on-change #(on-rule-field-change state idx :value (-> % .-target .-value))}])))

(defn- jira-field-input [rule state idx on-rule-field-change]
  (when-not (empty? (:type rule))
    [forms/input
     {:size "2"
      :placeholder "e.g. customfield_0410"
      :value (:jira_field rule)
      :not-margin-bottom? true
      :on-change #(on-rule-field-change state idx :jira_field (-> % .-target .-value))}]))

(defn- details-input [rule state idx on-rule-field-change]
  (when-not (empty? (:type rule))
    [forms/input
     {:size "2"
      :placeholder "e.g. customfield_0410"
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
  (let [toggle-all-non-tag-rules (fn []
                                   (on-toggle-all-mapping state is-not-connection-tag?))
        delete-non-tag-rules (fn []
                               (on-mapping-delete state is-not-connection-tag?))]
    [:> Box {:class "space-y-radix-5"}
     [:> Box
      [:> Table.Root {:size "2" :variant "surface"}
       [:> Table.Header
        [:> Table.Row {:align "center"}
         (when @select-state
           [:> Table.ColumnHeaderCell ""])
         [:> Table.ColumnHeaderCell "Type"]
         [:> Table.ColumnHeaderCell "Jira Field"]
         [:> Table.ColumnHeaderCell "Value"]
         [:> Table.ColumnHeaderCell "Description (Optional)"]]]

       [:> Table.Body
        (doall
         (for [[idx rule] (map-indexed vector @state)
               :when (is-not-connection-tag? rule)]
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
             [jira-field-input rule state idx on-mapping-field-change]]

            [:> Table.Cell {:p "4"}
             [value-field rule state idx on-mapping-field-change]]

            [:> Table.Cell {:p "4"}
             [details-input rule state idx on-mapping-field-change]]]))]]

      [:> Text {:as "p" :size "2" :mt "1" :class "text-[--gray-10]"}
       [:> Strong
        "Preset: "]
       "Relates hoop.dev fields with Jira fields. "
       [:> Strong
        "Custom: "]
       "Appends a custom key-value relation to Jira cards."]]

     [rule-buttons/main
      {:on-rule-add #(on-mapping-add state)
       :on-toggle-select #(on-toggle-mapping-select select-state)
       :select-state select-state
       :selected? (every? :selected (filter is-not-connection-tag? @state))
       :on-toggle-all toggle-all-non-tag-rules
       :on-rules-delete delete-non-tag-rules}]]))
