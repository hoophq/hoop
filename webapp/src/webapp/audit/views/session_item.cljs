(ns webapp.audit.views.session-item
  (:require
   [re-frame.core :as rf]
   [webapp.formatters :as formatters]
   [webapp.components.user-icon :as user-icon]
   [webapp.components.icon :as icon]
   [webapp.audit.views.session-details :as session-details]))

(defn session-item [session]
  (let [user-name (:user_name session)
        start-date (:start_date session)
        end-date (:end_date session)]
    [:div
     {:class (str "overflow-hidden border-b cursor-pointer hover:bg-gray-50"
                  " p-regular text-sm grid grid-col-3 lg:grid-cols-5 gap-regular lg:gap-large")
      :on-click (fn []
                  (rf/dispatch [:modal->open {:id "session-details"
                                              :maxWidth "none"
                                              :content [session-details/main session]}]))}

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
       [:b (:connection session)]]
      [:div
       {:class "text-xxs text-gray-800"}
       [:span (:type session)]]]
     [:div {:id "session-id" :class "flex gap-regular text-xs items-center"}
      [:span {:class "text-gray-400"}
       "id:"]
      [:span {:class "text-gray-800"}
       (take 8 (:id session))]]
     [:div#status-info {:class "col-span-2 flex flex-col-reverse lg:flex-row gap-regular justify-end"}
      (when (or (= end-date nil)
                (= end-date ""))
        [:div
         {:class "flex gap-small justify-end items-center h-full"}
         [:div {:class "rounded-full w-1.5 h-1.5 bg-green-500"}]
         [:span {:class "text-xxs text-gray-500"}
          "this session has pending items"]])
      [:div {:class (str "flex items-center gap-small justify-center text-xs"
                         " p-regular rounded-lg bg-gray-100 text-gray-800")}
       [icon/regular {:icon-name "watch-black"
                      :size 4}]
       [:span (formatters/time-ago-full-date start-date)]]]]))


