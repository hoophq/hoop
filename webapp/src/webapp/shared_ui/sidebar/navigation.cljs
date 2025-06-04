(ns webapp.shared-ui.sidebar.navigation
  (:require ["@heroicons/react/24/outline" :as hero-outline-icon]
            ["lucide-react" :refer [ChevronRight]]
            [re-frame.core :as rf]
            [webapp.config :as config]
            [webapp.shared-ui.sidebar.constants :as sidebar-constants]
            [webapp.shared-ui.sidebar.styles :as styles]
            [webapp.shared-ui.sidebar.components.nav-link :refer [nav-link]]
            [webapp.shared-ui.sidebar.components.section :refer [section-title]]
            [webapp.shared-ui.sidebar.components.profile :refer [profile-dropdown]]))

(defn main [_ _]
  (let [gateway-info (rf/subscribe [:gateway->info])
        current-route (rf/subscribe [:routes->route])]
    (fn [user]
      (let [gateway-version (:version (:data @gateway-info))
            auth-method (:auth_method (:data @gateway-info))
            user-data (:data user)
            admin? (:admin? user-data)
            free-license? (:free-license? user-data)
            current-route @current-route]
        [:<>
         [:div {:class "flex my-8 shrink-0 items-center"}
          [:figure {:class "w-40 cursor-pointer"}
           [:img {:src (str config/webapp-url
                            "/images/hoop-branding/PNG/hoop-symbol+text_white@4x.png")
                  :on-click #(rf/dispatch [:navigate :home])}]]]
         [:nav {:class "flex flex-1 flex-col"}
          [:ul {:role "list"
                :class "flex flex-1 flex-col gap-y-8"}
           [:li
            [:ul {:role "list" :class "space-y-1"}
             (for [route sidebar-constants/main-routes]
               ^{:key (:name route)}
               [nav-link {:uri (if (and free-license? (not (:free-feature? route)))
                                 "#"
                                 (:uri route))
                          :icon (:icon route)
                          :label (:name route)
                          :free-feature? (:free-feature? route)
                          :admin-only? (:admin-only? route)
                          :admin? admin?
                          :current-route current-route
                          :free-license? free-license?
                          :navigate (:navigate route)}])]]

           [:li
            [section-title "Discover"]
            [:ul {:role "list" :class "space-y-1 mt-2"}
             (for [route sidebar-constants/discover-routes
                   :when (not (and (:admin-only? route) (not admin?)))]
               ^{:key (:name route)}
               [:li
                [:a {:href (if (and free-license? (not (:free-feature? route)))
                             "#"
                             (:uri route))
                     :on-click (fn []
                                 (when (and free-license? (not (:free-feature? route)))
                                   (rf/dispatch [:navigate :upgrade-plan]))
                                 (when (:navigate route)
                                   (rf/dispatch [:navigate (:navigate route)])))
                     :class (str (styles/hover-side-menu-link (:uri route) current-route)
                                 (:enabled styles/link-styles)
                                 (when (and free-license? (not (:free-feature? route)))
                                   " text-opacity-30"))}
                 [:div {:class "flex gap-3 items-center w-full"}
                  [(:icon route) (when (and free-license? (not (:free-feature? route)))
                                   {:class "opacity-30"})]
                  (:label route)
                  (when (:badge route)
                    [:span {:class "ml-2 px-1.5 py-0.5 text-xs rounded bg-indigo-600 text-white font-medium"}
                     (:badge route)])]
                 (when (and free-license? (not (:free-feature? route)))
                   [:div {:class styles/badge-upgrade}
                    "Upgrade"])]])]]

           [:li
            [section-title "Settings"]
            [:ul {:role "list" :class "space-y-1 mt-2"}
             (for [route sidebar-constants/settings-routes
                   :when (not (and (:admin-only? route) (not admin?)))]
               ^{:key (:name route)}
               [:li
                [:a {:href (if (and free-license? (not (:free-feature? route)))
                             "#"
                             (:uri route))
                     :on-click (fn []
                                 (when (and free-license? (not (:free-feature? route)))
                                   (rf/dispatch [:navigate :upgrade-plan]))
                                 (when (:navigate route)
                                   (rf/dispatch [:navigate (:navigate route)])))
                     :class (str (styles/hover-side-menu-link (:uri route) current-route)
                                 (:enabled styles/link-styles)
                                 (when (and free-license? (not (:free-feature? route)))
                                   " text-opacity-30"))}
                 [:div {:class "flex gap-3 items-center"}
                  [(:icon route) (when (and free-license? (not (:free-feature? route)))
                                   {:class "opacity-30"})]
                  (:label route)]
                 (when (and free-license? (not (:free-feature? route)))
                   [:div {:class styles/badge-upgrade}
                    "Upgrade"])]])

             (when admin?
               [:li
                [:a {:href "#"
                     :on-click #(rf/dispatch [:navigate :integrations])
                     :class (str (styles/hover-side-menu-link "/integrations" current-route)
                                 (:enabled styles/link-styles))}
                 [:div {:class "flex gap-3 items-center"}
                  [:> hero-outline-icon/PuzzlePieceIcon {:class "h-6 w-6 shrink-0 text-white"
                                                         :aria-hidden "true"}]
                  "Integrations"]
                 [:> ChevronRight {:size 16 :class "text-white"}]]])]]

           [:li {:class "mt-auto mb-3"}
            [profile-dropdown {:user-data user-data
                               :auth-method auth-method
                               :gateway-version gateway-version}]]]]]))))
