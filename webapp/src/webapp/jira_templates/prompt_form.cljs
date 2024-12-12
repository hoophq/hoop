(ns webapp.jira-templates.prompt-form
  (:require
   ["@radix-ui/themes" :refer [Button Dialog Flex Box Text]]
   [reagent.core :as r]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]))

(defn main [{:keys [prompts on-submit]}]
  (let [form-data (r/atom {})]
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
         (for [prompt prompts]
           ^{:key (:jira_field prompt)}
           [forms/input
            {:label (:label prompt)
             :required (:required prompt)
             :placeholder (:label prompt)
             :value (get @form-data (:jira_field prompt) "")
             :on-change #(swap! form-data assoc (:jira_field prompt) (-> % .-target .-value))}])]

        [:> Flex {:justify "end" :gap "3" :mt "6"}
         [:> Button {:variant "soft"
                     :color "gray"
                     :type "button"
                     :on-click #(rf/dispatch [:modal->close])}
          "Cancel"]
         [:> Button {:type "submit"}
          "Send"]]]])))
