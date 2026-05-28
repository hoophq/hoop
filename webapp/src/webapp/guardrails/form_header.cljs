(ns webapp.guardrails.form-header
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout Flex Heading Link]]
   ["lucide-react" :refer [Info]]
   [webapp.components.button :as button]
   [re-frame.core :as rf]
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

         (when free-license?
           [:> Callout.Root {:size "1" :color "blue" :class "mx-7 mt-4" :highContrast true}
            [:> Callout.Icon
             [:> Info {:size 16}]]
            [:> Callout.Text
             "Organizations with Free plan have limited data protection. Upgrade to Enterprise to have unlimited access to Guardrails. "
             [:> Link {:href "#"
                       :class "font-medium"
                       :style {:color "var(--blue-12)"}
                       :on-click (fn [e]
                                   (.preventDefault e)
                                   (promotion/request-demo))}
              "Contact our Sales team \u2197"]]])]))))
