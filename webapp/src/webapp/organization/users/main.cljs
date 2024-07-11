(ns webapp.organization.users.main
  (:require [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.headings :as headings]
            [webapp.components.table :as table]
            [webapp.components.user-icon :as user-icon]
            [webapp.formatters :as formatters]
            [webapp.organization.users.form :as user-form]))

(defn list-item
  [item user-groups]
  (table/row
   [:div {:class "grid grid-cols-2"}
    [:div {:class "grid grid-cols-10"}
     [:div {:class "grid col-span-9"}
      [:small {:class "font-semibold text-gray-700 block"} (:name item)]
      [:small {:class "block text-gray-500"} (:email item)]]]
    [:div {:class "grid grid-cols-4 content-center"}
     [:small {:class "text-xs text-gray-500"}
      (formatters/list-to-comma-string (:groups item))]
     [:small {:class "text-xs text-gray-500"} (:status item)]

     [:div {:class "settings justify-self-end"}
      [:a
       {:class "block text-sm text-blue-500"
        :href "#"
        :on-click #(rf/dispatch [:open-modal [user-form/main :update item user-groups]])} "Edit"]]]]
   {:key (:id item)}))

(defn users-list
  [users user-groups]
  [:div
   (table/header
    [:div {:class "grid grid-cols-2"}
     [:div {:class "grid grid-cols-10"}
      [:small {:class "col-span-9"} "Name"]]
     [:div {:class "grid grid-cols-4 content-center"}
      [:small "Groups"]
      [:small "Status"]
      [:small ""]]]
    nil)

   (table/rows (doall (map #(list-item % user-groups) users)))])

(defn alert-multitenant-message []
  [:div {:class "border-l-4 border-yellow-400 bg-yellow-50 p-4 mb-regular"}
   [:div {:class "flex"}
    [:div {:class "flex-shrink-0"}
     [:svg {:class "h-5 w-5 text-yellow-400"
            :xmlns "http://www.w3.org/2000/svg"
            :viewBox "0 0 20 20"
            :fill "currentColor"
            :aria-hidden "true"}
      [:path {:fill-rule "evenodd"
              :d "M8.485 3.495c.673-1.167 2.357-1.167 3.03 0l6.28 10.875c.673 1.167-.17 2.625-1.516 2.625H3.72c-1.347 0-2.189-1.458-1.515-2.625L8.485 3.495zM10 6a.75.75 0 01.75.75v3.5a.75.75 0 01-1.5 0v-3.5A.75.75 0 0110 6zm0 9a1 1 0 100-2 1 1 0 000 2z"
              :clip-rule "evenodd"}]]]
    [:div {:class "ml-3"}
     [:p {:class "text-sm text-yellow-700"}
      "You are using a custom IDP so users state and groups from this
       list are not being used. Go to your IDP to setup groups and change groups statuses."]]]])

(defn section-header
  [users user-groups]
  [:div {:class "grid grid-cols-1 lg:grid-cols-4 gap-small lg:gap-0 items-center mb-large"}
   [headings/h2 (str (count users) " Member" (if (< (count users) 2) "" "s"))]
   [:div {:class "search-bar lg:col-span-2"}]
   [:div
    [button/primary {:text "Create user"
                     :full-width true
                     :on-click #(rf/dispatch [:open-modal [user-form/main :create nil user-groups]])}]]])

(defn main []
  (let [current-user (rf/subscribe [:users->current-user])
        users (rf/subscribe [:users])
        user-groups (rf/subscribe [:user-groups])
        alert-multitenant-active? (r/atom (-> @current-user :data :is_multitenant))]

    (rf/dispatch [:users->get-user-groups])
    (rf/dispatch [:users->get-users])
    (fn []
      (js/setTimeout #(reset! alert-multitenant-active? false) 7000)
      [:div {:class "bg-white rounded-lg h-full p-6 overflow-y-auto"}
       [:section
        [section-header @users @user-groups]
        (when @alert-multitenant-active? [alert-multitenant-message])
        [:div {:class "hidden lg:block"}
         [users-list @users @user-groups]]
        [:div {:class "block lg:hidden rounded-lg border"}
         (for [user @users]
           ^{:key (:id user)}
           [:div
            {:class (str "overflow-hidden border-b cursor-pointer hover:bg-gray-50"
                         " p-regular text-sm grid grid-col-3 gap-regular")
             :on-click #(rf/dispatch [:open-modal [user-form/main :update user @user-groups]])}

            [:div {:id "user-info"
                   :class "flex items-center justify-between"}
             [:div {:class "flex gap-small items-center"}
              [user-icon/initials-black (:name user)]
              [:div
               {:class "text-gray-800 text-xs"}
               (:name user)]]
             [:div
              {:class "text-xxs text-gray-800"}
              [:span (:status user)]]]
            [:div {:id "connection-info"
                   :class "flex w-42 flex-col gap-small items-start"}
             [:div
              [:small {:class "text-xs text-gray-500"}
               (formatters/list-to-comma-string (:groups user))]]]])]]])))
