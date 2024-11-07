(ns webapp.guardrails.form-header
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading]]
   ["lucide-react" :refer [ArrowLeft]]
   [re-frame.core :as rf]))

(defn main [{:keys [form-type id scroll-pos on-save]}]
  [:> Box
   [:> Flex {:p "5" :gap "2"}
    [:> Button {:variant "ghost"
                :size "2"
                :color "gray"
                :on-click #(js/history.back)}
     [:> ArrowLeft {:size 16}]
     "Back"]]
   [:> Box {:p "7"}
    [:> Flex {:justify "between"
              :align "center"
              :pb "7"
              :class (str "sticky top-0 z-10 -m-10 mb-0 p-10 bg-gray-1 "
                          (when (>= @scroll-pos 30)
                            "border-b border-[--gray-a6]"))}
     [:> Heading {:as "h2" :size "8"}
      "Create a new Guardrail"]
     [:> Flex {:gap "5" :align "center"}
      (when (= :editor-plugin form-type)
        [:> Button {:size "4"
                    :variant "ghost"
                    :color "red"
                    :on-click #(rf/dispatch [:guardrails->delete-by-id id])}
         "Delete"])
      [:> Button {:size "4"
                  :on-click on-save}
       "Save"]]]]])
