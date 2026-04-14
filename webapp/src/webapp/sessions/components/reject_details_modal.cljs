(ns webapp.sessions.components.reject-details-modal
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text]]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

(defn main [{:keys [on-confirm on-cancel]}]
  (let [comment (r/atom "")]
    (fn []
      [:> Box {:class "w-full space-y-radix-7"}
       [:> Box {:class "space-y-radix-2"}
        [:> Heading {:as "h1" :size "7" :weight "bold" :class "text-gray-12"}
         "Reject Details"]
        [:> Text {:as "p" :size "3" :class "text-gray-11"}
         "Optionally include more details for this access request rejection."]]

       [forms/textarea {:label "Comment"
                        :placeholder "Share the details here..."
                        :value @comment
                        :on-change #(reset! comment (-> % .-target .-value))}]

       [:> Flex {:justify "between" :align "center"}
        [:> Button {:size "3"
                    :variant "soft"
                    :color "gray"
                    :type "button"
                    :on-click on-cancel}
         "Cancel"]
        [:> Button {:size "3"
                    :type "button"
                    :on-click #(on-confirm {:comment @comment})}
         "Confirm and Reject"]]])))
