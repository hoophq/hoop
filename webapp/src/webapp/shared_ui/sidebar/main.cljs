(ns webapp.shared-ui.sidebar.main
  (:require ["@headlessui/react" :as ui]
            ["@heroicons/react/24/outline" :as hero-outline-icon]
            ["@radix-ui/themes" :refer [ScrollArea]]
            ["lucide-react" :refer [ChevronsRight ChevronsLeft Puzzle Settings]]
            ["react" :as react]
            [re-frame.core :as rf]
            [webapp.components.theme-provider :refer [theme-provider]]
            [webapp.components.user-icon :as user-icon]
            [webapp.config :as config]
            [webapp.shared-ui.sidebar.constants :as constants]
            [webapp.shared-ui.sidebar.navigation :as navigation]
            [webapp.shared-ui.sidebar.styles :as styles]))

(defn mobile-sidebar [_ _ _]
  (let [sidebar-mobile (rf/subscribe [:sidebar-mobile])]
    (fn [user my-plugins]
      (let [sidebar-open? (if (= :opened (:status @sidebar-mobile))
                            true
                            false)]
        [:<>
         ;; sidebar opened
         [:> ui/Transition {:show sidebar-open?
                            :as react/Fragment}
          [:> ui/Dialog {:as "div"
                         :class "relative z-40 lg:hidden"
                         :onClose #(rf/dispatch [:sidebar-mobile->close])}
           [theme-provider
            [:<>
             [:> (.-Child ui/Transition) {:as react/Fragment
                                          :enter (:mobile-enter styles/transitions)
                                          :enterFrom (:mobile-enter-from styles/transitions)
                                          :enterTo (:mobile-enter-to styles/transitions)
                                          :leave (:mobile-leave styles/transitions)
                                          :leaveFrom (:mobile-leave-from styles/transitions)
                                          :leaveTo (:mobile-leave-to styles/transitions)}
              [:div {:class "fixed inset-0 bg-[#182449] bg-opacity-80"}]]

             [:div {:class (:mobile styles/sidebar-container)}
              [:> (.-Child ui/Transition) {:as react/Fragment
                                           :enter (:slide-enter styles/transitions)
                                           :enterFrom (:slide-enter-from styles/transitions)
                                           :enterTo (:slide-enter-to styles/transitions)
                                           :leave (:slide-leave styles/transitions)
                                           :leaveFrom (:slide-leave-from styles/transitions)
                                           :leaveTo (:slide-leave-to styles/transitions)}
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
                [:div {:class "flex grow flex-col gap-y-5 overflow-y-auto bg-[#182449] px-6 pb-4 ring-1 ring-white ring-opacity-10"}
                 [navigation/main user my-plugins]]]]]]]]]
         ;; end sidebar opened

         ;; sidebar closed
         [:div {:class "sticky top-0 z-30 flex items-center justify-between gap-x-6 bg-[#182449] px-4 py-3 shadow-sm sm:px-6 lg:hidden"}
          [:button {:type "button"
                    :class "-m-2.5 p-2.5 text-gray-700 lg:hidden"
                    :onClick #(rf/dispatch [:sidebar-mobile->open])}
           [:span {:class "sr-only"} "Open sidebar"]
           [:> hero-outline-icon/Bars3Icon {:class (:standard styles/icon-styles)
                                            :aria-hidden "true"}]]]
         ;; sidebar closed
         ]))))

(defn desktop-sidebar [_ _]
  (let [sidebar-desktop (rf/subscribe [:sidebar-desktop])
        current-route (rf/subscribe [:routes->route])]
    (fn [user my-plugins]
      (let [user-data (:data user)
            admin? (:admin? user-data)
            free-license? (:free-license? user-data)
            sidebar-open? (if (= :opened (:status @sidebar-desktop))
                            true
                            false)
            current-route @current-route]
        [:<>
         ;; sidebar opened
         (when sidebar-open?
          [:> ui/Transition {:show true
                             :enter (:fade-enter styles/transitions)
                             :enterFrom (:fade-enter-from styles/transitions)
                             :enterTo (:fade-enter-to styles/transitions)
                             :leave (:fade-leave styles/transitions)
                             :leaveFrom (:fade-leave-from styles/transitions)
                             :leaveTo (:fade-leave-to styles/transitions)}
           [:div {:class (:desktop styles/sidebar-container)}
            [:> ScrollArea {:class "h-[calc(100%-2.5rem)] flex grow flex-col"}
             [:div {:class "flex flex-col gap-y-2 px-4 pb-10"}
              [navigation/main user my-plugins]]]
            [:div {:class "w-full py-2 px-2 absolute bottom-0 bg-[#182449] dark border-t border-primary-5 hover:bg-primary-5 hover:text-white cursor-pointer flex justify-end z-10"
                   :onClick #(rf/dispatch [:sidebar-desktop->close])}
             [:> ChevronsLeft {:size 24
                               :color "white"
                               :aria-hidden "true"}]]]])
         ;; end sidebar opened

         ;; sidebar closed
         (when-not sidebar-open?
          [:div {:class (:collapsed styles/sidebar-container)}
          [:> ScrollArea {:class "h-[calc(100%-2.5rem)] flex grow flex-col overflow-x-hidden bg-[#182449]"}
           [:div {:class "flex flex-col gap-y-2 px-2 pb-10 w-full max-w-full"}
            [:div {:class "flex my-8 shrink-0 items-center justify-center w-full"}
             [:figure {:class "cursor-pointer w-full flex justify-center"}
              [:img {:src (str config/webapp-url
                               "/images/hoop-branding/SVG/hoop-symbol+text_white.svg")
                     :class "max-w-full h-auto"
                     :on-click #(rf/dispatch [:navigate :home])}]]]
            [:nav {:class "flex flex-1 flex-col"}
             [:ul {:role "list"
                   :class "flex flex-1 items-center flex-col gap-y-16"}

              ;; Main Routes (collapsed)
              [:li
               [:ul {:role "list"
                     :class "flex flex-col items-center space-y-1"}
                (for [route constants/main-routes]
                  ^{:key (:name route)}
                  [:li {:class (str (when
                                     (and (:admin-only? route) (not admin?)) "hidden"))}
                   [:a {:href (if (and free-license? (not (:free-feature? route)))
                                "#"
                                (:uri route))
                        :on-click (fn []
                                    (when (and free-license? (not (:free-feature? route)))
                                      (rf/dispatch [:navigate :upgrade-plan]))
                                    (when (:action route)
                                      ((:action route))))
                        :class (str (styles/hover-side-menu-link (:uri route) current-route)
                                    "group flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold"
                                    (when (and free-license? (not (:free-feature? route)))
                                      " text-opacity-30")
                                    (when (some? (:action route)) " cursor-pointer"))}
                    [(:icon route) {:class (str (:standard styles/icon-styles)
                                                (when (and free-license? (not (:free-feature? route)))
                                                  " opacity-30"))
                                    :aria-hidden "true"}]
                    [:span {:class "sr-only"}
                     (:name route)]]])]]

              ;; Discover Section (collapsed)
              [:li
               [:ul {:role "list"
                     :class "flex flex-col items-center space-y-1"}
                (for [route constants/discover-routes
                      :when (not (and (:admin-only? route) (not admin?)))]
                  ^{:key (:name route)}
                  [:li
                   [:a {:href (if (and free-license? (not (:free-feature? route)))
                                "#"
                                (:uri route))
                        :on-click (fn []
                                    (when (and free-license? (not (:free-feature? route)))
                                      (rf/dispatch [:navigate :upgrade-plan])))
                        :class (str (styles/hover-side-menu-link (:uri route) current-route)
                                    "group flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold"
                                    (when (and free-license? (not (:free-feature? route)))
                                      " text-opacity-30"))}
                    [(:icon route) {:class (str (:standard styles/icon-styles)
                                                (when (and free-license? (not (:free-feature? route)))
                                                  " opacity-30"))
                                    :aria-hidden "true"}]
                    [:span {:class "sr-only"}
                     (:label route)]]])]]

              ;; Settings Section (collapsed)
              [:li
               [:ul {:role "list"
                     :class "flex flex-col items-center space-y-1"}
                (for [route constants/organization-routes
                      :when (not (and (:admin-only? route) (not admin?)))]
                  ^{:key (:name route)}
                  [:li
                   [:a {:href (if (and free-license? (not (:free-feature? route)))
                                "#"
                                (:uri route))
                        :on-click (fn []
                                    (when (and free-license? (not (:free-feature? route)))
                                      (rf/dispatch [:navigate :upgrade-plan])))
                        :class (str (styles/hover-side-menu-link (:uri route) current-route)
                                    "group flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold"
                                    (when (and free-license? (not (:free-feature? route)))
                                      " text-opacity-30"))}
                    [(:icon route) {:class (str (:standard styles/icon-styles)
                                                (when (and free-license? (not (:free-feature? route)))
                                                  " opacity-30"))
                                    :aria-hidden "true"}]
                    [:span {:class "sr-only"}
                     (:label route)]]])

                ;; Integrations (com ícone especial)
                (when admin?
                  [:li
                   [:a {:href "#"
                        :on-click #(rf/dispatch [:sidebar-desktop->open])
                        :class "text-gray-300 hover:text-white hover:bg-white/5 group items-start flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold"}
                    [:> Puzzle {:size 24
                                :aria-hidden "true"}]
                    [:span {:class "sr-only"}
                     "Integrations"]]])

                (when admin?
                  [:li
                   [:a {:href "#"
                        :on-click #(rf/dispatch [:sidebar-desktop->open])
                        :class "text-gray-300 hover:text-white hover:bg-white/5 group items-start flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold"}
                    [:> Settings {:size 24
                                  :aria-hidden "true"}]
                    [:span {:class "sr-only"}
                     "Settings"]]])]]

              ;; User profile (always at bottom)
              [:li {:class "mt-auto mb-3"}
               [:a {:href "#"
                    :onClick #(rf/dispatch [:sidebar-desktop->open])
                    :class "text-gray-400 hover:text-white hover:bg-white/5 group flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold"}
                [user-icon/initials-white (:name user-data)]]]]]]]
           [:div {:class "w-full py-2 px-2 absolute bottom-0 bg-[#182449] dark border-t border-primary-5 hover:bg-primary-5 hover:text-white cursor-pointer flex justify-center z-10"
                  :onClick #(rf/dispatch [:sidebar-desktop->open])}
            [:> ChevronsRight {:size 24
                               :color "white"
                               :aria-hidden "true"}]]])
         ;; end sidebar closed
         ]))))

(defn container []
  (let [user (rf/subscribe [:users->current-user])
        my-plugins (rf/subscribe [:plugins->my-plugins])]

    (when (empty? (:data @user))
      (rf/dispatch [:users->get-user]))

    (rf/dispatch [:plugins->get-my-plugins])

    (fn []
      (if (empty? (:data @user))
        [:<>]
        [:div
         [mobile-sidebar @user @my-plugins]
         [desktop-sidebar @user @my-plugins]]))))

(defn main [_]
  (let [sidebar-desktop (rf/subscribe [:sidebar-desktop])]
    (fn [panels]
      [:div
       [container]
       [:main {:class (if (= :opened (:status @sidebar-desktop))
                        "h-screen bg-[#182449] w-full absolute lg:pl-side-menu-width"
                        "h-screen bg-[#182449] w-full absolute lg:pl-[72px]")}
        panels]])))
