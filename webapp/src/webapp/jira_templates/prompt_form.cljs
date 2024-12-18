(ns webapp.jira-templates.prompt-form
  (:require
   ["@radix-ui/themes" :refer [Button Flex Box Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

(defn- create-cmdb-select-options [jira-values]
  (mapv (fn [{:keys [id name]}]
          {:value id :text name})
        jira-values))

(defn- get-value-id [jira_values value]
  (when-let [value-match (first (filter #(= (:name %) value) jira_values))]
    (:id value-match)))

(defn- init-form-data [cmdb-items]
  (reduce (fn [acc {:keys [jira_field jira_values value]}]
            (if-let [id (get-value-id jira_values value)]
              (assoc-in acc [:jira_fields jira_field] id)
              acc))
          {}
          cmdb-items))

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
                            (on-submit @form-data))}

        [:> Flex {:direction "column" :gap "4"}
         ;; Prompt Fields
         (when (seq prompts)
           [:> Box {:class "space-y-4"}
            [:> Text {:as "h4" :size "3" :weight "medium" :mb "2"} "Command Information"]
            (for [{:keys [label required jira_field]} prompts]
              ^{:key jira_field}
              [forms/input
               {:label label
                :required required
                :placeholder label
                :on-change #(swap! form-data assoc-in [:jira_fields jira_field] (.. % -target -value))}])])

         ;; CMDB Fields - Apenas mostrar campos que precisam de seleção
         (when-let [cmdb-fields-to-show (seq (filter (fn [{:keys [value jira_values]}]
                                                       (and jira_values
                                                            (not (some #(= (:name %) value) jira_values))))
                                                     cmdb-items))]
           [:> Box {:class "space-y-4"}
            [:> Text {:as "h4" :size "3" :weight "medium" :mb "2"} "CMDB Information"]
            (doall
             (for [{:keys [description jira_field jira_values required]} cmdb-fields-to-show]
               ^{:key jira_field}
               [forms/select
                {:label description
                 :required required
                 :full-width? true
                 :selected (get-in @form-data [:jira_fields jira_field])
                 :on-change #(swap! form-data assoc-in [:jira_fields jira_field] %)
                 :options (create-cmdb-select-options jira_values)}]))])]

        [:> Flex {:justify "end" :gap "3" :mt "6"}
         [:> Button {:variant "soft"
                     :color "gray"
                     :type "button"
                     :on-click #(rf/dispatch [:modal->close])}
          "Cancel"]
         [:> Button {:type "submit"}
          "Send"]]]])))
