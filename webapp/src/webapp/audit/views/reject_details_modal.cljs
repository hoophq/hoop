(ns webapp.audit.views.reject-details-modal
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Switch Text]]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

(defn main [{:keys [user on-confirm on-cancel]}]
  (let [comment (r/atom "")
        add-username? (r/atom false)]
    (fn []
      [:> Box {:class "w-full space-y-radix-7"}
       [:> Box
        [:> Heading {:as "h1" :size "6" :weight "bold" :class "text-gray-12"}
         "Reject Details"]
        [:> Text {:as "p" :size "3" :class "text-gray-11"}
         "Optionally include more details or identification for this access request rejection."]]

       [forms/textarea {:label "Comment"
                        :placeholder "Share the details here..."
                        :value @comment
                        :on-change #(reset! comment (-> % .-target .-value))}]

       [:> Flex {:align "start" :gap "3"}
        [:> Switch {:checked @add-username?
                    :on-checked-change #(reset! add-username? %)}]
        [:> Box
         [:> Text {:size "2" :weight "medium" :class "text-gray-12" :as "p"} "Add username"]
         [:> Text {:size "2" :class "text-gray-11" :as "p"}
          "Include your username to the details when rejecting this access request."]]]

       [:> Flex {:justify "between" :align "center"}
        [:> Button {:size "3"
                    :variant "ghost"
                    :color "gray"
                    :type "button"
                    :on-click on-cancel}
         "Cancel"]
        [:> Button {:size "3"
                    :type "button"
                    :on-click #(on-confirm {:comment @comment
                                            :add-username? @add-username?
                                            :user-name (-> user :name)})}
         "Confirm and Reject"]]])))
