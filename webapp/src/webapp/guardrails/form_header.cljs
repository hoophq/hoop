(ns webapp.guardrails.form-header
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading]]
   [webapp.components.button :as button]
   [re-frame.core :as rf]
   [webapp.features.activation-journey.views.enterprise-banner :as enterprise-banner]
   [webapp.features.promotion :as promotion]))

(defn main [_]
  (let [user (rf/subscribe [:users->current-user])]
    (fn [{:keys [form-type id scroll-pos]}]
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
              "Configure Guardrail"
              "Create a new Guardrail")]
           [:> Flex {:gap "5" :align "center"}
            (when (= :edit form-type)
              [:> Button {:size "4"
                          :variant "ghost"
                          :color "red"
                          :type "button"
                          :on-click #(rf/dispatch [:guardrails->delete-by-id id])}
               "Delete"])
            [:> Button {:size "4"
                        :type "submit"}
             "Save"]]]]

         ;; Free-plan upsell pinned below the header, non-dismissible.
         (when free-license?
           [:> Box {:class "mx-7 mt-4"}
            [enterprise-banner/main
             {:primary {:label "Talk to Sales"
                        :on-click promotion/request-demo}}]])]))))
