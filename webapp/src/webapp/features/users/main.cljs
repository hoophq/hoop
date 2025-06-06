(ns webapp.features.users.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.config :as config]
   [webapp.features.promotion :as promotion]
   [webapp.features.users.views.empty-state :as empty-state]
   [webapp.features.users.views.user-form :as user-form]
   [webapp.features.users.views.user-list :as user-list]))

(defn main []
  (let [users (rf/subscribe [:users])
        user-groups (rf/subscribe [:user-groups])
        user (rf/subscribe [:users->current-user])
        promotion-seen (rf/subscribe [:users/promotion-seen])
        min-loading-done (r/atom false)]

    (rf/dispatch [:users->get-users])
    (rf/dispatch [:users->get-user-groups])

    (js/setTimeout #(reset! min-loading-done true) 1500)

    (fn []
      (let [current-user (:data @user)
            user-count (count @users)
            has-users? (> user-count 0)
            single-user? (= user-count 1)
            loading? (or (= :loading (:status @user))
                         (not @min-loading-done))]

        (cond
          loading?
          [:> Box {:class "bg-gray-1 h-full"}
           [:> Flex {:direction "column" :justify "center" :align "center" :height "100%"}
            [loaders/simple-loader]]]

          (and single-user?
               (not @promotion-seen))
          [:> Box {:class "bg-gray-1 h-full"}
           [promotion/users-promotion {:mode :empty-state}]]

          :else
          [:> Box {:class "flex flex-col bg-white px-4 py-10 sm:px-6 lg:p-radix-7 h-full"}
           [:> Flex {:direction "column" :gap "7" :class "h-full"}

            [:> Flex {:justify "between" :align "center"}
             [:> Box
              [:> Heading {:as "h1" :size "8" :weight "bold" :class "text-gray-12"}
               "Users"]
              [:> Text {:as "p" :size "2" :weight "medium" :class "text-gray-11"}
               (str (count @users) " Member" (if (< (count @users) 2) "" "s"))]]
             (when (and (:user-management? current-user)
                        (not single-user?))
               [:> Button {:size "3"
                           :onClick #(rf/dispatch [:modal->open
                                                   {:content [user-form/main :create nil @user-groups]
                                                    :maxWidth "450px"}])}
                "Add User"])]

            [:> Box {:class "flex-grow"}
             [:> Box {:class "h-full"}
              (cond
                (not has-users?)
                [empty-state/main]

                single-user?
                [:> Flex {:direction "column" :gap "6" :class "h-full"}
                 [user-list/main]

                 [:> Flex {:direction "column" :justify "center" :align "center" :gap "5" :class "pt-6 h-full"}
                  [:> Text {:as "p" :size "3" :class "text-gray-11 text-center max-w-md"}
                   "Invite users and setup team-based permissions and approval workflows for secure resource access"]

                  (when (:user-management? current-user)
                    [:> Button {:size "3"
                                :onClick #(rf/dispatch [:modal->open
                                                        {:content [user-form/main :create nil @user-groups]
                                                         :maxWidth "450px"}])}
                     "Invite Users"])]

                 [:> Text {:as "p" :size "2" :class "text-gray-11 text-center"}
                  "Need more information? Check out "
                  [:a {:href (-> config/docs-url :clients :web-app :managing-accesss) :class "text-blue-600 hover:underline"}
                   "User Management documentation"]
                  "."]]

                :else
                [user-list/main])]]]])))))
