(ns webapp.jira-templates.prompt-form
  (:require
   ["@radix-ui/themes" :refer [Button Flex Box Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multi-select]))

(defn- create-cmdb-select-options [jira-values]
  (mapv (fn [{:keys [id name]}]
          {"value" id "label" name})
        jira-values))

(defn- get-value-id [jira_values value]
  (when-let [value-match (first (filter #(= (:name %) value) jira_values))]
    {"value" (:id value-match) "label" (:name value-match)}))

(defn- init-form-data [cmdb-items]
  (reduce (fn [acc {:keys [jira_field jira_values value]}]
            (if-let [id (get-value-id jira_values value)]
              (assoc-in acc [:jira_fields jira_field] id)
              acc))
          {}
          cmdb-items))

(defn- process-datetime-field [prompts field-key field-value]
  (let [prompt (first (filter #(= (:jira_field %) field-key) prompts))]
    (if (and (= (:field_type prompt) "datetime-local")
             (not (empty? field-value)))
      (-> (js/Date. field-value)
          .toISOString)
      field-value)))

(defn main [{:keys [prompts cmdb-items on-submit]}]
  (let [form-data (r/atom (init-form-data cmdb-items))]
    (fn []
      [:> Box {:class "p-6"}
       [:> Text {:as "h3" :size "5" :weight "bold" :mb "4"}
        "Required information"]
       [:> Text {:as "p" :size "2" :mb "5"}
        "Fill out following information to proceed with your command request."]

       [:form {:on-submit (fn [e]
                            (.preventDefault e)
                            (let [processed-data
                                  (update @form-data :jira_fields
                                          (fn [fields]
                                            (reduce-kv
                                             (fn [m k v]
                                               (assoc m k
                                                      (cond
                                                        (map? v) (get v "value")
                                                        :else (process-datetime-field prompts k v))))
                                             {}
                                             fields)))]
                              (on-submit processed-data)))}

        [:> Flex {:direction "column" :gap "4"}
         ;; Prompt Fields
         (when (seq prompts)
           [:> Box {:class "space-y-4"}
            (for [{:keys [label required jira_field field_type]} prompts]
              ^{:key jira_field}
              [forms/input
               {:label label
                :required required
                :type field_type
                :placeholder label
                :on-change #(swap! form-data assoc-in [:jira_fields jira_field] (.. % -target -value))}])])

         ;; CMDB Fields - Apenas mostrar campos que precisam de seleção
         (when-let [cmdb-fields-to-show (seq (filter (fn [{:keys [value jira_values]}]
                                                       (and jira_values
                                                            (not (some #(= (:name %) value) jira_values))))
                                                     cmdb-items))]
           [:> Box {:class "space-y-4"}
            (doall
             (for [{:keys [label jira_field jira_values required]} cmdb-fields-to-show]
               ^{:key jira_field}
               [multi-select/single
                {:label label
                 :required required
                 :placeholder "Select one"
                 :id (str "cmdb-select-" label)
                 :name (str "cmdb-select-" label)
                 :clearable? true
                 :searchble? true
                 :default-value (or (get-in @form-data [:jira_fields jira_field]) nil)
                 :on-change #(swap! form-data assoc-in [:jira_fields jira_field] (js->clj %))
                 :options (create-cmdb-select-options jira_values)}]))])]

        [:> Flex {:justify "end" :gap "3" :mt "6"}
         [:> Button {:variant "soft"
                     :color "gray"
                     :type "button"
                     :on-click #(rf/dispatch [:modal->close])}
          "Cancel"]
         [:> Button {:type "submit"}
          "Send"]]]])))
