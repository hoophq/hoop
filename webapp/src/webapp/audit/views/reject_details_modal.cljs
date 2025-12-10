(ns webapp.audit.views.reject-details-modal
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text TextArea Switch]]
   [reagent.core :as r]))

(defn main [{:keys [on-confirm on-cancel]}]
  (let [description (r/atom "")
        include-username? (r/atom false)]
    (fn [{:keys [on-confirm on-cancel]}]
      [:> Box {:class "w-full"}
       [:> Text {:size "6" :weight "bold" :class "mb-2"}
        "Reject Details"]
       [:> Text {:size "2" :class "mb-6 text-gray-600"}
        "Describe a reason for this request rejection."]

       [:> Box {:class "mb-6"}
        [:> Text {:size "2" :weight "bold" :class "mb-2"}
         "Description"]
        [:> TextArea {:placeholder "Share the details here..."
                      :value @description
                      :on-change #(reset! description (-> % .-target .-value))
                      :rows 4
                      :class "w-full"}]]

       [:> Box {:class "mb-6"}
        [:> Flex {:align "center" :gap "3"}
         [:> Switch {:checked @include-username?
                     :on-checked-change #(reset! include-username? %)}]
         [:> Box
          [:> Text {:size "2" :weight "bold"}
           "Add username"]
          [:> Text {:size "1" :class "text-gray-600"}
           "Include your username to the details when rejecting this access request."]]]]

       [:> Flex {:justify "end" :gap "3" :mt "6"}
        [:> Button {:variant "soft"
                    :on-click on-cancel}
         "Cancel"]
        [:> Button {:color "red"
                    :on-click #(on-confirm {:description @description
                                            :include-username @include-username?})}
         "Confirm and Reject"]]])))

