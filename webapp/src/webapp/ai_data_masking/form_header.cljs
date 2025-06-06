(ns webapp.ai-data-masking.form-header
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading]]
   [re-frame.core :as rf]
   [webapp.components.button :as button]))

(defn main [{:keys [form-type id scroll-pos loading?]}]
  (let [form-title (if (= :edit form-type)
                     "Edit AI Data Masking rule"
                     "Create new AI Data Masking rule")]
    [:<>
     [:> Flex {:p "5" :gap "2"}
      [button/HeaderBack]]
     [:> Box {:class (str "sticky top-0 z-50 bg-gray-1 px-7 py-7 "
                          (when (>= @scroll-pos 30)
                            "border-b border-[--gray-a6]"))}
      [:> Flex {:justify "between"
                :align "center"}
       [:> Heading {:size "7" :weight "bold" :class "text-[--gray-12]"}
        form-title]

       [:> Flex {:gap "5" :align "center"}
        (when (= :edit form-type)
          [:> Button {:size "4"
                      :variant "ghost"
                      :color "red"
                      :disabled loading?
                      :type "button"
                      :on-click #(rf/dispatch [:jira-templates->delete-by-id id])}
           "Delete"])
        [:> Button {:size "3"
                    :variant "solid"
                    :type "submit"
                    :form "ai-data-masking-form"
                    :disabled loading?
                    :loading loading?}
         "Save"]]]]]))
