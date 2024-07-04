(ns webapp.reviews.review-item
  (:require
   [re-frame.core :as rf]
   [webapp.components.user-icon :as user-icon]))

(defmulti review-item identity)

(defmethod review-item :default [status review]
  (let [user-email (-> review :review_owner :email)]
    [:div
     {:key (str (:id review) (:group review))
      :class (str "overflow-hidden border-b cursor-pointer hover:bg-gray-100 px-regular py-small"
                  (when (= status :selected) " bg-gray-100"))
      :on-click #(rf/dispatch [:reviews-plugin->get-review-by-id review])}
     [:div
      {:class "flex mb-regular"}
      [:div
       {:class "flex flex-grow gap-small items-center"}
       [:span
        {:class "text-sm text-gray-800 font-semibold"}
        (-> review :review_connection :name)]
       [:span
        {:class "text-xxs text-gray-500"}
        (:type review)]]
      [:div
       {:class "flex gap-small text-xs"}
       [:span
        {:class "text-gray-500"}
        "session id:"]
       [:span (take 8 (:session review))]]]
     [:div
      {:class "flex"}
      [:div#user-info
       {:class "flex flex-grow items-center"}
       [:div {:class "flex gap-small items-center"}
        [:div
         {:class "flex items-center gap-small text-gray-800 text-xs"}
         [user-icon/email-black user-email]
         [:span user-email]]]]
      [:div
       [:div
        {:class "flex gap-small items-end text-xs"}
        [:span
         {:class "text-gray-600"}
         "status:"]
        [:span
         {:class "text-xxs"}
         (:status review)]]]]]))

