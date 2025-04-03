(ns webapp.reviews.review-item
  (:require
   [re-frame.core :as rf]
   [webapp.components.user-icon :as user-icon]
   [webapp.components.icon :as icon]
   [webapp.formatters :as formatters]
   [webapp.reviews.review-detail :as review-detail]))

(defmulti review-item identity)

(defmethod review-item :default [status session]
  (let [review (:review session)
        user-name (:user_name session)
        connection-name (:connection session)]
    [:div
     {:key (str (:id session) (-> review :id))
      :class (str "overflow-hidden border-b cursor-pointer hover:bg-gray-50"
                  " p-regular text-sm grid grid-col-3 lg:grid-cols-5 gap-regular lg:gap-large"
                  (when (= status :selected) " bg-gray-100"))
      :on-click #(rf/dispatch [:reviews-plugin->get-session-details (:id session)])}

     [:div {:id "user-info"
            :class "flex items-center"}
      [:div {:class "truncate flex gap-small items-center"}
       [:div
        [user-icon/initials-black user-name]]
       [:div
        {:class "truncate text-gray-800 text-xs"}
        user-name]]]

     [:div {:id "connection-info"
            :class "flex w-42 flex-col gap-small items-end lg:items-start"}
      [:div
       [:b connection-name]]
      [:div
       {:class "text-xxs text-gray-800"}
       [:span (:type session)]]]

     [:div {:id "session-id" :class "flex gap-regular text-xs items-center"}
      [:span {:class "text-gray-400"}
       "id:"]
      [:span {:class "text-gray-800"}
       (take 8 (:id session))]]

     [:div#status-info {:class "col-span-2 flex flex-col-reverse lg:flex-row gap-regular justify-end"}
      [:div {:class (str "flex items-center gap-small justify-center text-xs"
                         " p-regular rounded-lg bg-gray-100 text-gray-800")}
       [icon/regular {:icon-name "watch-black"
                      :size 4}]
       [:span (formatters/time-parsed->full-date (:start_date session))]
       [:div {:class "ml-small flex items-center gap-small"}
        [:span {:class "text-gray-600"}
         "status:"]
        [:span
         {:class (str "text-xxs rounded-full px-1 py-0.5 "
                      (case (-> review :status)
                        "PENDING" "bg-yellow-100 text-yellow-800"
                        "APPROVED" "bg-green-100 text-green-800"
                        "REJECTED" "bg-red-100 text-red-800"
                        "bg-gray-100 text-gray-800"))}
         (-> review :status)]]]]]))

