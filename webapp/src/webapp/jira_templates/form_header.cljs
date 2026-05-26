(ns webapp.jira-templates.form-header
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading]]
   [re-frame.core :as rf]
   [webapp.components.button :as button]
   [webapp.shared-ui.free-license-banner :as free-license-banner]))

(defn main [_]
  (let [user (rf/subscribe [:users->current-user])]
    (fn [{:keys [form-type loading? id scroll-pos]}]
      (let [free-license? (-> @user :data :free-license?)]
        [:<>
         [:> Flex {:p "5" :gap "2"}
          [button/HeaderBack]]
         [:> Box {:class (str "sticky top-0 z-50 bg-gray-1 px-7 py-7 "
                              (when (>= @scroll-pos 30)
                                "border-b border-[--gray-a6]"))}
          [:> Flex {:justify "between"
                    :align "center"}
           [:> Heading {:as "h2" :size "8"}
            (if (= :edit form-type)
              "Configure JIRA Template"
              "Create a new JIRA Template")]
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
                        :loading loading?
                        :disabled loading?
                        :type "submit"}
             "Save"]]]]

         (when free-license?
           [free-license-banner/main
            {:class "mx-7 mt-4"
             :message (str "Organizations with Free plan have limited automation. "
                           "Upgrade to Enterprise to have unlimited access to Jira Templates.")}])]))))
