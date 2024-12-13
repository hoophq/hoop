(ns webapp.jira-templates.prompt-form
  (:require
   ["@radix-ui/themes" :refer [Button Flex Box Text]]
   [reagent.core :as r]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]))

(defn main [{:keys [prompts on-submit]}]
  (let [form-data (r/atom {})]
    (fn []
      (println prompts)
      [:> Box {:class "p-6"}
       [:> Text {:as "h3" :size "5" :weight "bold" :mb "4"}
        "Required information"]
       [:> Text {:as "p" :size "2" :mb "5"}
        "Fill out following information to proceed with your command request."]

       [:form {:on-submit (fn [e]
                            (.preventDefault e)
                            (on-submit @form-data))}

        [:> Flex {:direction "column" :gap "4"}
         (for [{:keys [label required jira_field]} prompts]
           ^{:key jira_field}
           [forms/input
            {:label label
             :required required
             :placeholder label
             :on-change (fn [e]
                          (.log js/console e)
                          (let [value (-> e .-target .-value)]
                            (swap! form-data assoc jira_field value)))}])]

        [:> Flex {:justify "end" :gap "3" :mt "6"}
         [:> Button {:variant "soft"
                     :color "gray"
                     :type "button"
                     :on-click #(rf/dispatch [:modal->close])}
          "Cancel"]
         [:> Button {:type "submit"}
          "Send"]]]])))
