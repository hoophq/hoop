(ns webapp.ai-data-masking.form-header
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout Flex Heading Link]]
   ["lucide-react" :refer [Info]]
   [re-frame.core :as rf]
   [webapp.components.button :as button]
   [webapp.features.promotion :as promotion]))

(defn main [{:keys [form-type id scroll-pos loading?]}]
  (let [user (rf/subscribe [:users->current-user])
        form-title (if (= :edit form-type)
                     "Edit AI Data Masking rule"
                     "Create new AI Data Masking rule")
        free-license? (-> @user :data :free-license?)]
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
                      :on-click #(rf/dispatch [:dialog->open
                                               {:title "Delete AI Data Masking rule?"
                                                :text "This action will permanently delete this AI Data Masking rule and cannot be undone. Are you sure you want to proceed?"
                                                :text-action-button "Delete"
                                                :action-button? true
                                                :type :danger
                                                :on-success (fn []
                                                              (rf/dispatch [:ai-data-masking->delete-by-id id]))}])}
           "Delete"])
        [:> Button {:size "3"
                    :variant "solid"
                    :type "submit"
                    :form "ai-data-masking-form"
                    :disabled loading?
                    :loading loading?}
         "Save"]]]]

     (when free-license?
       [:> Callout.Root {:size "1" :color "blue" :class "mx-7 mt-4" :highContrast true}
        [:> Callout.Icon
         [:> Info {:size 16}]]
        [:> Callout.Text
         "Organizations with Free plan have limited data protection. Upgrade to Enterprise to have unlimited access to AI Data Masking. "
         [:> Link {:href "#"
                   :class "font-medium"
                   :style {:color "var(--blue-12)"}
                   :on-click (fn [e]
                               (.preventDefault e)
                               (promotion/request-demo))}
          "Contact our Sales team \u2197"]]])]))
