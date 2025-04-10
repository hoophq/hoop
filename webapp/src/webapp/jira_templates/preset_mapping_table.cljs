(ns webapp.jira-templates.preset-mapping-table
  (:require
   ["@radix-ui/themes" :refer [Box Table Text Strong]]
   [webapp.components.forms :as forms]
   [webapp.jira-templates.rule-buttons :as rule-buttons]
   [clojure.string :as str]))

(def tag-value-options
  [{:value "session.connection_tags.env" :text "Environment Tag"}
   {:value "session.connection_tags.team" :text "Team Tag"}
   {:value "session.connection_tags.product" :text "Product Tag"}
   {:value "session.connection_tags.service" :text "Service Tag"}])

(defn- is-connection-tag? [rule]
  (and (:value rule)
       (str/starts-with? (:value rule) "session.connection_tags.")))

(defn- jira-field-input [rule state idx on-rule-field-change]
  [forms/input
   {:size "2"
    :placeholder "e.g. customfield_0410"
    :value (:jira_field rule)
    :not-margin-bottom? true
    :on-change #(on-rule-field-change state idx :jira_field (-> % .-target .-value))}])

(defn- value-field [rule state idx on-rule-field-change]
  [forms/select
   {:size "2"
    :variant "ghost"
    :not-margin-bottom? true
    :on-change #(on-rule-field-change state idx :value %)
    :selected (:value rule)
    :full-width? true
    :options tag-value-options}])

(defn- details-input [rule state idx on-rule-field-change]
  [forms/input
   {:size "2"
    :placeholder "e.g. Environment"
    :value (:description rule)
    :not-margin-bottom? true
    :on-change #(on-rule-field-change state idx :description (-> % .-target .-value))}])

(defn main [{:keys [state
                    select-state
                    on-mapping-field-change
                    on-mapping-select
                    on-toggle-mapping-select
                    on-toggle-all-mapping
                    on-mapping-delete
                    on-mapping-add]}]
  (let [add-preset-rule (fn []
                          (on-mapping-add state (fn [rule]
                                                  (assoc rule
                                                         :type "preset"
                                                         :value "session.connection_tags.env"))))
        toggle-all-preset-rules (fn []
                                  (on-toggle-all-mapping state is-connection-tag?))
        delete-preset-rules (fn []
                              (on-mapping-delete state is-connection-tag?))]
    [:> Box {:class "space-y-radix-5"}
     [:> Box
      [:> Table.Root {:size "2" :variant "surface"}
       [:> Table.Header
        [:> Table.Row {:align "center"}
         (when @select-state
           [:> Table.ColumnHeaderCell ""])
         [:> Table.ColumnHeaderCell "Tag"]
         [:> Table.ColumnHeaderCell "Jira Field"]
         [:> Table.ColumnHeaderCell "Description (Optional)"]]]

       [:> Table.Body
        (doall
         (for [[idx rule] (map-indexed vector @state)
               :when (is-connection-tag? rule)]
           ^{:key idx}
           [:> Table.Row {:align "center"}
            (when @select-state
              [:> Table.RowHeaderCell {:p "2" :width "20px"}
               [:input {:type "checkbox"
                        :checked (:selected rule)
                        :on-change #(on-mapping-select state idx)}]])

            [:> Table.Cell {:p "4"}
             [value-field rule state idx on-mapping-field-change]]

            [:> Table.Cell {:p "4"}
             [jira-field-input rule state idx on-mapping-field-change]]

            [:> Table.Cell {:p "4"}
             [details-input rule state idx on-mapping-field-change]]]))]]

      [:> Text {:as "p" :size "2" :mt "1" :class "text-[--gray-10]"}
       "Relate connection tags with Jira fields for automated mapping."]]

     [rule-buttons/main
      {:on-rule-add add-preset-rule
       :on-toggle-select #(on-toggle-mapping-select select-state)
       :select-state select-state
       :selected? (every? :selected (filter is-connection-tag? @state))
       :on-toggle-all toggle-all-preset-rules
       :on-rules-delete delete-preset-rules}]]))
