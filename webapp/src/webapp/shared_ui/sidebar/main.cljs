(ns webapp.shared-ui.sidebar.main
  (:require ["@headlessui/react" :as ui]
            ["@heroicons/react/24/outline" :as hero-outline-icon]
            ["react" :as react]
            [re-frame.core :as rf]
            [webapp.components.user-icon :as user-icon]
            [webapp.config :as config]
            [webapp.shared-ui.sidebar.constants :as constants]
            [webapp.shared-ui.sidebar.navigation :as navigation]
            [webapp.routes :as routes]))

(defn mobile-sidebar [_ _ _]
  (let [sidebar-mobile (rf/subscribe [:sidebar-mobile])]
    (fn [user my-plugins]
      (let [user-data (:data user)
            admin? (:admin? user-data)
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
                                        :enter "transition-opacity ease-linear duration-500"
                                        :enterFrom "opacity-0"
                                        :enterTo "opacity-100"
                                        :leave "transition-opacity ease-linear duration-500"
                                        :leaveFrom "opacity-100"
                                        :leaveTo "opacity-0"}
            [:div {:class "fixed inset-0 bg-[#060E1D] bg-opacity-80"}]]

           [:div {:class "fixed inset-0 flex"}
            [:> (.-Child ui/Transition) {:as react/Fragment
                                         :enter "transition ease-in-out duration-700 transform"
                                         :enterFrom "-translate-x-full"
                                         :enterTo "translate-x-0"
                                         :leave "transition ease-in-out duration-700 transform"
                                         :leaveFrom "translate-x-0"
                                         :leaveTo "-translate-x-full"}
             [:> (.-Panel ui/Dialog) {:class "relative mr-16 flex w-full max-w-xs flex-1"}
              [:> (.-Child ui/Transition) {:as react/Fragment
                                           :enter "transition ease-in-out duration-700 transform"
                                           :enterFrom "opacity-0"
                                           :enterTo "opacity-100"
                                           :leave "transition ease-in-out duration-700 transform"
                                           :leaveFrom "opacity-100"
                                           :leaveTo "opacity-0"}
               [:div {:class "absolute left-full top-0 flex w-16 justify-center pt-5"}
                [:button {:type "button"
                          :class "-m-2.5 p-2.5"
                          :onClick #(rf/dispatch [:sidebar-mobile->close])}
                 [:span.sr-only "Close sidebar"]
                 [:> hero-outline-icon/XMarkIcon {:class "h-6 w-6 shrink-0 text-white"
                                                  :aria-hidden "true"}]]]]
              [:div {:class "flex grow flex-col gap-y-5 overflow-y-auto bg-[#060E1D] px-6 pb-4 ring-1 ring-white ring-opacity-10"}
               [navigation/main user my-plugins]]]]]]]
   ;; end sidebar opened

   ;; sidebar closed
         [:div {:class "sticky top-0 z-30 flex items-center justify-between gap-x-6 bg-[#060E1D] px-4 py-3 shadow-sm sm:px-6 lg:hidden"}
          [:button {:type "button"
                    :class "-m-2.5 p-2.5 text-gray-700 lg:hidden"
                    :onClick #(rf/dispatch [:sidebar-mobile->open])}
           [:span {:class "sr-only"} "Open sidebar"]
           [:> hero-outline-icon/Bars3Icon {:class "h-6 w-6 shrink-0 text-white"
                                            :aria-hidden "true"}]]]
   ;; sidebar closed
         ]))))

(defn hover-side-menu-link? [uri-item current-route]
  (if (= uri-item current-route)
    "bg-gray-800 text-white "
    "hover:bg-gray-800 hover:text-white text-gray-300 "))

(defn desktop-sidebar [_ _]
  (let [sidebar-desktop (rf/subscribe [:sidebar-desktop])
        current-route (rf/subscribe [:routes->route])]
    (fn [user my-plugins]
      (let [user-data (:data user)
            plugins-enabled (filterv (fn [plugin]
                                       (some #(= (:name plugin) (:name %)) my-plugins)) constants/plugins-routes)
            admin? (:admin? user-data)
            free-license? (:free-license? user-data)
            sidebar-open? (if (= :opened (:status @sidebar-desktop))
                            true
                            false)
            current-route @current-route]
        [:<>
       ;; sidebar opened
         [:> ui/Transition {:show sidebar-open?
                            :as react/Fragment
                            :enter "transition-opacity duration-400 ease-in-out transform"
                            :enterFrom "opacity-0"
                            :enterTo "opacity-100"
                            :leave "transition-opacity duration-400 ease-in-out transform"
                            :leaveFrom "opacity-100"
                            :leaveTo "opacity-0"}
          [:div {:class "hidden lg:fixed lg:inset-y-0 lg:z-40 lg:flex lg:w-side-menu lg:flex-col lg:bg-[#060E1D]"}
           [:div {:class "border-t border-gray-800 w-full py-2 px-2 absolute bottom-0 bg-[#060E1D] hover:bg-gray-800 hover:text-white cursor-pointer flex justify-end"
                  :onClick #(rf/dispatch [:sidebar-desktop->close])}
            [:> hero-outline-icon/ChevronDoubleLeftIcon {:class "h-6 w-6 shrink-0 text-white"
                                                         :aria-hidden "true"}]]
           [:div {:class "h-full flex grow flex-col gap-y-2 overflow-y-auto bg-[#060E1D] px-4 pb-10"}
            [navigation/main user my-plugins]]]]
       ;; end sidebar opened

       ;; sidebar closed
         [:div {:class "hidden lg:fixed lg:inset-y-0 lg:left-0 lg:z-30 lg:block lg:w-[72px] lg:overflow-y-auto lg:bg-[#060E1D]"}
          [:div {:class "border-t bg-[#060E1D] border-gray-800 w-full py-2 px-2 absolute bottom-0 bg-[#060E1D] hover:bg-gray-800 hover:text-white cursor-pointer flex justify-center"
                 :onClick #(rf/dispatch [:sidebar-desktop->open])}
           [:> hero-outline-icon/ChevronDoubleRightIcon {:class "h-6 w-6 shrink-0 text-white"
                                                         :aria-hidden "true"}]]
          [:div {:class "h-full flex grow flex-col gap-y-2 overflow-y-auto bg-[#060E1D] px-4 pb-10"}
           [:div {:class "flex my-8 shrink-0 items-center justify-center"}
            [:figure {:class "cursor-pointer"}
             [:img {:src (str config/webapp-url
                              "/images/hoop-branding/SVG/hoop-symbol+text_white.svg")
                    :on-click #(rf/dispatch [:navigate :home])}]]]
           [:nav {:class "flex flex-1 flex-col"}
            [:ul {:role "list"
                  :class "flex flex-1 items-center flex-col gap-y-6"}
             [:li
              [:ul {:role "list"
                    :class "flex flex-col items-center space-y-1"}
               (for [route constants/routes]
                 ^{:key (:name route)}
                 [:li {:class (str (when
                                    (and (:admin-only? route) (not admin?)) "hidden"))}
                  [:a {:href (if (and free-license? (not (:free-feature? route)))
                               "#"
                               (:uri route))
                       :on-click (fn []
                                   (when (and free-license? (not (:free-feature? route)))
                                     (js/window.Intercom
                                      "showNewMessage"
                                      "I want to upgrade my current plan")))
                       :class (str (hover-side-menu-link? (:uri route) current-route)
                                   "group flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold"
                                   (when (and free-license? (not (:free-feature? route)))
                                     " text-opacity-30"))}
                   [(:icon route) {:class (str "h-6 w-6 shrink-0 text-white"
                                               (when (and free-license? (not (:free-feature? route)))
                                                 " opacity-30"))
                                   :aria-hidden "true"}]
                   [:span {:class "sr-only"}
                    (:name route)]]])

               (for [plugin plugins-enabled]
                 ^{:key (:name plugin)}
                 [:li
                  [:a {:href (:uri plugin)
                       :class "text-gray-400 hover:text-white hover:bg-gray-800 group flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold"}
                   [(:icon plugin) {:class (str "h-6 w-6 shrink-0 text-white")
                                    :aria-hidden "true"}]
                   [:span {:class "sr-only"}
                    (:label plugin)]]])]]

             [:ul {:class "space-y-1 mt-6"}
              [:li
               [:a {:href (routes/url-for :connections)
                    :class (str (hover-side-menu-link? "/connections" current-route)
                                "group items-start flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold")}
                [:div {:class "flex gap-3 items-center"}
                 [:> hero-outline-icon/ArrowsRightLeftIcon {:class "h-6 w-6 shrink-0 text-white"
                                                            :aria-hidden "true"}]
                 [:span {:class "sr-only"}
                  "Connections"]]]]

              (when admin?
                [:li
                 [:a {:href (routes/url-for :users)
                      :class (str (hover-side-menu-link? "/organization/users" current-route)
                                  "group items-start flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold")}
                  [:> hero-outline-icon/UserGroupIcon {:class "h-6 w-6 shrink-0 text-white"
                                                       :aria-hidden "true"}]
                  [:span {:class "sr-only"}
                   "Users"]]])

              [:li
               [:a {:href (routes/url-for :guardrails)
                    :class (str (hover-side-menu-link? "/guardrails" current-route)
                                "group items-start flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold")}
                [:> hero-outline-icon/ShieldCheckIcon {:class "h-6 w-6 shrink-0 text-white"
                                                     :aria-hidden "true"}]
                [:span {:class "sr-only"}
                 "Guardrails"]]]
              [:li
               [:a {:href (routes/url-for :agents)
                    :class (str (hover-side-menu-link? "/agents" current-route)
                                "group items-start flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold")}
                [:> hero-outline-icon/ServerStackIcon {:class "h-6 w-6 shrink-0 text-white"
                                                       :aria-hidden "true"}]
                [:span {:class "sr-only"}
                 "Agents"]]]
              (when admin?
                [:li
                 [:a {:href "#"
                      :on-click #(rf/dispatch [:sidebar-desktop->open])
                      :class "text-gray-400 hover:text-white hover:bg-gray-800 group items-start flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold"}
                  [:> hero-outline-icon/Cog8ToothIcon {:class "h-6 w-6 shrink-0 text-white"
                                                       :aria-hidden "true"}]
                  [:span {:class "sr-only"}
                   "Settings"]]])]

             [:li {:class "mt-auto mb-3"}
              [:a {:href "#"
                   :onClick #(rf/dispatch [:sidebar-desktop->open])
                   :class "text-gray-400 hover:text-white hover:bg-gray-800 group flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold"}
               [user-icon/initials-white (:name user-data)]]]]]]]
       ;; end sidebar closed
         ]))))

(defn container []
  (let [user (rf/subscribe [:users->current-user])
        my-plugins (rf/subscribe [:plugins->my-plugins])]
    (when (empty? (:data @user))
      (rf/dispatch [:users->get-user]))

    (rf/dispatch [:plugins->get-my-plugins])
    (rf/dispatch [:connections->get-connections])
    (js/Canny "identify"
              #js{:appID config/canny-id
                  :user #js{:email (some-> @user :data :email)
                            :name (some-> @user :data :name)
                            :id (some-> @user :data :id)}})
    (fn []
      [:div
       [mobile-sidebar @user @my-plugins]
       [desktop-sidebar @user @my-plugins]])))

(defn main [_]
  (let [sidebar-desktop (rf/subscribe [:sidebar-desktop])]
    (fn [panels]
      [:div
       [container]
       [:main {:class (if (= :opened (:status @sidebar-desktop))
                        "h-screen bg-[#060E1D] w-full absolute lg:pl-side-menu-width"
                        "h-screen bg-[#060E1D] w-full absolute lg:pl-[72px]")}
        panels]])))
