(ns webapp.shared-ui.sidebar.main
  (:require ["@headlessui/react" :as ui]
            ["@heroicons/react/24/outline" :as hero-outline-icon]
            ["@heroicons/react/20/solid" :as hero-solid-icon]
            ["react" :as react]
            [re-frame.core :as rf]
            [webapp.components.user-icon :as user-icon]
            [webapp.config :as config]
            [webapp.connections.constants :as connection-constants]
            [webapp.shared-ui.sidebar.connection-overlay :as connection-overlay]
            [webapp.shared-ui.sidebar.constants :as constants]
            [webapp.shared-ui.sidebar.navigation :as navigation]))

(defn mobile-sidebar [_ _ _]
  (let [sidebar-mobile (rf/subscribe [:sidebar-mobile])]
    (fn [user my-plugins connection]
      (let [user-data (:data user)
            admin? (:admin? user-data)
            connection-data (:data connection)
            sidebar-open? (if (= :opened (:status @sidebar-mobile))
                            true
                            false)]
        [:<>
   ;; sidebar opened
         [:> ui/Transition {:show sidebar-open?
                            :as react/Fragment}
          [:> ui/Dialog {:as "div"
                         :class "relative z-40 lg:hidden"
                         :onClose #(rf/dispatch [:sidebar-mobile->close])}
           [:> (.-Child ui/Transition) {:as react/Fragment
                                        :enter "transition-opacity ease-linear duration-400"
                                        :enterFrom "opacity-0"
                                        :enterTo "opacity-100"
                                        :leave "transition-opacity ease-linear duration-400"
                                        :leaveFrom "opacity-100"
                                        :leaveTo "opacity-0"}
            [:div {:class "fixed inset-0 bg-gray-900 bg-opacity-80"}]]

           [:div {:class "fixed inset-0 flex"}
            [:> (.-Child ui/Transition) {:as react/Fragment
                                         :enter "transition ease-in-out duration-400 transform"
                                         :enterFrom "-translate-x-full"
                                         :enterTo "translate-x-0"
                                         :leave "transition ease-in-out duration-400 transform"
                                         :leaveFrom "translate-x-0"
                                         :leaveTo "-translate-x-full"}
             [:> (.-Panel ui/Dialog) {:class "relative mr-16 flex w-full max-w-xs flex-1"}
              [:> (.-Child ui/Transition) {:as react/Fragment
                                           :enter "ease-in-out duration-400"
                                           :enterFrom "opacity-0"
                                           :enterTo "opacity-100"
                                           :leave "ease-in-out duration-400"
                                           :leaveFrom "opacity-100"
                                           :leaveTo "opacity-0"}
               [:div {:class "absolute left-full top-0 flex w-16 justify-center pt-5"}
                [:button {:type "button"
                          :class "-m-2.5 p-2.5"
                          :onClick #(rf/dispatch [:sidebar-mobile->close])}
                 [:span.sr-only "Close sidebar"]
                 [:> hero-outline-icon/XMarkIcon {:class "h-6 w-6 shrink-0 text-white"
                                                  :aria-hidden "true"}]]]]
              [:div {:class "flex grow flex-col gap-y-5 overflow-y-auto bg-gray-900 px-6 pb-4 ring-1 ring-white ring-opacity-10"}
               [navigation/main user my-plugins]]]]]]]
   ;; end sidebar opened

   ;; sidebar closed
         [:div {:class "sticky top-0 z-30 flex items-center justify-between gap-x-6 bg-gray-900 px-4 py-3 shadow-sm sm:px-6 lg:hidden"}
          [:button {:type "button"
                    :class "-m-2.5 p-2.5 text-gray-700 lg:hidden"
                    :onClick #(rf/dispatch [:sidebar-mobile->open])}
           [:span {:class "sr-only"} "Open sidebar"]
           [:> hero-outline-icon/Bars3Icon {:class "h-6 w-6 shrink-0 text-white"
                                            :aria-hidden "true"}]]
          [:button {:on-click #(reset! connection-overlay/overlay-open? true)
                    :class "overflow-ellipsis text-white bg-gray-800 hover:bg-blue-600 group items-center flex justify-between rounded-md p-2 text-sm leading-6 font-semibold"}
           [:div {:class "flex gap-3 justify-start items-center"}
            [:figure {:class "w-5"}
             [:img {:src (connection-constants/get-connection-icon connection-data :dark)
                    :class "w-9"}]]
            [:span {:class "text-left truncate w-32"}
             (:name connection-data)]]]]
   ;; sidebar closed
         ]))))

(defn hover-side-menu-link? [uri-item current-route]
  (if (= uri-item current-route)
    "bg-gray-800 text-white "
    "hover:bg-gray-800 hover:text-white text-gray-300 "))

(defn desktop-sidebar [_ _]
  (let [sidebar-desktop (rf/subscribe [:sidebar-desktop])
        current-route (rf/subscribe [:routes->route])]
    (fn [user my-plugins connection]
      (let [user-data (:data user)
            connection-data (:data connection)
            plugins-enabled (filterv (fn [plugin]
                                       (some #(= (:name plugin) (:name %)) my-plugins)) constants/plugins-routes)
            admin? (:admin? user-data)
            sidebar-open? (if (= :opened (:status @sidebar-desktop))
                            true
                            false)
            current-route @current-route]
        [:<>
       ;; sidebar opened
         [:> ui/Transition {:show sidebar-open?
                            :as react/Fragment
                            :enter "transition-opacity duration-75 ease-out"
                            :enterFrom "opacity-0"
                            :enterTo "opacity-100"
                            :leave "transition-opacity duration-100 ease-out"
                            :leaveFrom "opacity-100"
                            :leaveTo "opacity-0"}
          [:div {:class "hidden lg:fixed lg:inset-y-0 lg:z-40 lg:flex lg:w-side-menu lg:flex-col lg:bg-gray-900"}
           [:div {:class "border-t border-gray-800 w-full py-2 px-2 absolute bottom-0 bg-gray-900 hover:bg-gray-800 hover:text-white cursor-pointer flex justify-end"
                  :onClick #(rf/dispatch [:sidebar-desktop->close])}
            [:> hero-outline-icon/ChevronDoubleLeftIcon {:class "h-6 w-6 shrink-0 text-white"
                                                         :aria-hidden "true"}]]
           [:div {:class "h-full flex grow flex-col gap-y-2 overflow-y-auto bg-gray-900 px-4 pb-10"}
            [navigation/main user my-plugins]]]]
       ;; end sidebar opened

       ;; sidebar closed
         [:div {:class "hidden lg:fixed lg:inset-y-0 lg:left-0 lg:z-30 lg:block lg:w-14 lg:overflow-y-auto lg:bg-gray-900"}
          [:div {:class "border-t bg-gray-900 border-gray-800 w-full py-2 px-2 absolute bottom-0 bg-gray-900 hover:bg-gray-800 hover:text-white cursor-pointer flex justify-center"
                 :onClick #(rf/dispatch [:sidebar-desktop->open])}
           [:> hero-outline-icon/ChevronDoubleRightIcon {:class "h-6 w-6 shrink-0 text-white"
                                                         :aria-hidden "true"}]]
          [:div {:class "h-full flex grow flex-col gap-y-2 overflow-y-auto bg-gray-900 px-4 pb-10"}
           [:div {:class "flex h-16 shrink-0 items-center justify-center"}
            [:figure {:class "w-5 cursor-pointer"}
             [:img {:src "/images/hoop-branding/PNG/hoop-symbol_white@4x.png"
                    :on-click #(rf/dispatch [:navigate :home])}]]]
           [:nav {:class "mt-8 flex flex-1 flex-col"}
            [:ul {:role "list"
                  :class "flex flex-1 items-center flex-col gap-y-6"}
             [:li
              [:ul {:role "list"
                    :class "flex flex-col items-center space-y-1"}
               [:li
                (if connection-data
                  [:button {:on-click #(rf/dispatch [:sidebar-desktop->open])
                            :class "w-full overflow-ellipsis text-white bg-gray-800 group items-center flex justify-between rounded-md p-2 text-sm leading-6 font-semibold mb-6"}
                   [:div {:class "flex gap-3 justify-start items-center"}
                    [:figure {:class "w-5"}
                     [:img {:src (connection-constants/get-connection-icon connection-data :dark)
                            :class "w-9"}]]
                    [:span {:class "sr-only"}
                     (:name connection-data)]]]

                  [:button {:on-click #(rf/dispatch [:sidebar-desktop->open])
                            :class "w-full overflow-ellipsis text-white bg-blue-500 hover:bg-blue-800 group items-center flex justify-between rounded-md p-2 text-sm leading-6 font-semibold mb-6"}
                   [:div {:class "flex gap-3 justify-start items-center"}
                    [:figure {:class "w-5"}
                     [:> hero-solid-icon/PlusIcon {:class "h-5 w-5 text-white"
                                                   :aria-hidden "true"}]]
                    [:span {:class "sr-only"}
                     "Add connection"]]])]

               (for [route constants/routes]
                 ^{:key (:name route)}
                 [:li
                  [:a {:href (if (and (:need-connection? route) (not connection-data))
                               "#"
                               (:uri route))
                       :class (str (hover-side-menu-link? (:uri route) current-route)
                                   "group flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold")}
                   [(:icon route) {:class (str "h-6 w-6 shrink-0 text-white"
                                               (when (and (:need-connection? route) (not connection-data))
                                                 " opacity-30"))
                                   :aria-hidden "true"}]
                   [:span {:class "sr-only"}
                    (:name route)]]])

               (for [plugin plugins-enabled]
                 ^{:key (:name plugin)}
                 [:li
                  [:a {:href (if (and (:need-connection? plugin) (not connection-data))
                               "#"
                               (:uri plugin))
                       :class "text-gray-400 hover:text-white hover:bg-gray-800 group flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold"}
                   [(:icon plugin) {:class (str "h-6 w-6 shrink-0 text-white"
                                                (when (and (:need-connection? plugin) (not connection-data))
                                                  " opacity-30"))
                                    :aria-hidden "true"}]
                   [:span {:class "sr-only"}
                    (:label plugin)]]])]]

             (when (and admin? (seq my-plugins))
               [:li {:class "mt-8"}
                [:a {:href "/organization/users"
                     :class (str (hover-side-menu-link? "/organization/users" current-route)
                                 "group items-start flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold")}
                 [:> hero-outline-icon/UserGroupIcon {:class "h-6 w-6 shrink-0 text-white"
                                                      :aria-hidden "true"}]
                 [:span {:class "sr-only"}
                  "Users"]]

                [:a {:href "#"
                     :on-click #(rf/dispatch [:sidebar-desktop->open])
                     :class "text-gray-400 hover:text-white hover:bg-gray-800 group items-start flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold"}
                 [:> hero-outline-icon/Cog8ToothIcon {:class "h-6 w-6 shrink-0 text-white"
                                                      :aria-hidden "true"}]
                 [:span {:class "sr-only"}
                  "Settings"]]])

             [:li {:class "mt-auto mb-3"}
              [:a {:href "#"
                   :onClick #(rf/dispatch [:sidebar-desktop->open])
                   :class "text-gray-400 hover:text-white hover:bg-gray-800 group flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold"}
               [user-icon/initials-white (:name user-data)]]]]]]]
       ;; end sidebar closed
         ]))))

(defn container []
  (let [user (rf/subscribe [:users->current-user])
        my-plugins (rf/subscribe [:plugins->my-plugins])
        context-connection (rf/subscribe [:connections->context-connection])]
    (when (empty? (:data @user))
      (rf/dispatch [:users->get-user]))

    (when (empty? (:data @context-connection))
      (rf/dispatch [:connections->fullfill-context-connection]))

    (rf/dispatch [:plugins->get-my-plugins])
    (rf/dispatch [:connections->get-connections])
    (js/Canny "identify"
              #js{:appID config/canny-id
                  :user #js{:email (some-> @user :data :email)
                            :name (some-> @user :data :name)
                            :id (some-> @user :data :id)}})
    (fn []
      [:div
       [connection-overlay/main @user @context-connection]
       [mobile-sidebar @user @my-plugins @context-connection]
       [desktop-sidebar @user @my-plugins @context-connection]])))

(defn main [_]
  (let [sidebar-desktop (rf/subscribe [:sidebar-desktop])]
    (fn [panels]
      [:div
       [container]
       [:main {:class (if (= :opened (:status @sidebar-desktop))
                        "h-screen w-full absolute lg:pl-side-menu-width"
                        "h-screen w-full absolute lg:pl-14")}
        panels]])))
