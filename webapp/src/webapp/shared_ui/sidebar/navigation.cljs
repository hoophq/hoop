(ns webapp.shared-ui.sidebar.navigation
  (:require
   ["@headlessui/react" :as ui]
   ["@radix-ui/themes" :refer [Badge]]
   ["lucide-react" :refer [ChevronDown ChevronRight Puzzle Settings]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.config :as config]
   [webapp.shared-ui.sidebar.components.nav-link :refer [nav-link]]
   [webapp.shared-ui.sidebar.components.profile :refer [profile-dropdown]]
   [webapp.shared-ui.sidebar.components.section :refer [section-title]]
   [webapp.shared-ui.sidebar.constants :as sidebar-constants]
   [webapp.shared-ui.sidebar.styles :as styles]))

(defn main [_ _]
  (let [gateway-info (rf/subscribe [:gateway->info])
        current-route (rf/subscribe [:routes->route])
        sidebar-mobile (rf/subscribe [:sidebar-mobile])]
    (fn [user]
      (let [gateway-version (:version (:data @gateway-info))
            auth-method (:auth_method (:data @gateway-info))
            user-data (:data user)
            admin? (:admin? user-data)
            selfhosted? (= (:tenancy_type user-data) "selfhosted")
            free-license? (:free-license? user-data)
            current-route @current-route
            is-mobile? (= :opened (:status @sidebar-mobile))]
        [:<>
         [:div {:class "flex my-8 shrink-0 items-center"}
          [:a {:href "/"
               :aria-label "Go to Home"
               :on-click (fn [e]
                           (.preventDefault e)
                           (rf/dispatch [:navigate :home])
                           (when is-mobile?
                             (rf/dispatch [:sidebar-mobile->close])))
               :class "w-40 cursor-pointer rounded-md"}
           [:img {:src (str config/webapp-url
                            "/images/hoop-branding/PNG/hoop-symbol+text_white@4x.png")
                  :alt ""
                  :aria-hidden "true"}]]]
         [:nav {:class "flex flex-1 flex-col"
                :aria-label "Primary"}
          [:ul {:role "list"
                :class "flex flex-1 flex-col gap-y-8"}
           [:li
            [:ul {:role "list" :class "space-y-1"}
             (for [route sidebar-constants/main-routes]
               ^{:key (:name route)}
               [nav-link {:uri (:uri route)
                          :icon (:icon route)
                          :label (:name route)
                          :free-feature? (:free-feature? route)
                          :admin-only? (:admin-only? route)
                          :admin? admin?
                          :current-route current-route
                          :free-license? free-license?
                          :action (when (:action route)
                                    (fn []
                                      ((:action route))
                                      (when is-mobile?
                                        (rf/dispatch [:sidebar-mobile->close]))))
                          :navigate (:navigate route)
                          :badge (:badge route)}])]]

           (when admin?
             [:li
              [section-title "Discover" "sidebar-discover-heading"]
              [:ul {:role "list"
                    :aria-labelledby "sidebar-discover-heading"
                    :class "space-y-1 mt-2"}
               (for [route sidebar-constants/discover-routes]
                 ^{:key (:name route)}
                 [nav-link {:uri (:uri route)
                            :icon (:icon route)
                            :label (:label route)
                            :free-feature? (:free-feature? route)
                            :admin-only? (:admin-only? route)
                            :admin? admin?
                            :current-route current-route
                            :free-license? free-license?
                            :navigate (:navigate route)
                            :badge (:badge route)
                            :upgrade-plan-route (:upgrade-plan-route route)
                            :on-activate (when is-mobile?
                                           #(rf/dispatch [:sidebar-mobile->close]))}])]])

           (when admin?
             [:li
              [section-title "Organization" "sidebar-organization-heading"]
              [:ul {:role "list"
                    :aria-labelledby "sidebar-organization-heading"
                    :class "space-y-1 mt-2"}
               (for [route sidebar-constants/organization-routes]
                 ^{:key (:name route)}
                 [nav-link {:uri (:uri route)
                            :icon (:icon route)
                            :label (:label route)
                            :free-feature? (:free-feature? route)
                            :admin-only? (:admin-only? route)
                            :admin? admin?
                            :current-route current-route
                            :free-license? free-license?
                            :navigate (:navigate route)
                            :upgrade-plan-route (:upgrade-plan-route route)
                            :on-activate (when is-mobile?
                                           #(rf/dispatch [:sidebar-mobile->close]))}])

               (when admin?
                 [:> ui/Disclosure {:as "li"
                                    :class "text-xs font-semibold leading-6 text-gray-400"}
                  (fn [params]
                    (r/as-element
                     [:<>
                      [:> (.-Button ui/Disclosure) {:class (str "w-full group flex items-center justify-between rounded-md p-2 text-sm "
                                                                "font-semibold leading-6 text-gray-300 hover:bg-white/5 hover:text-white")
                                                    :aria-label (if (.-open params)
                                                                  "Collapse Integrations section"
                                                                  "Expand Integrations section")}
                       [:div {:class "flex gap-3 justify-start items-center"}
                        [:> Puzzle {:size 24
                                    :aria-hidden "true"}]
                        "Integrations"]
                       (if (.-open params)
                         [:> ChevronDown {:size 24
                                          :aria-hidden "true"}]
                         [:> ChevronRight {:size 24
                                           :aria-hidden "true"}])]
                      [:> (.-Panel ui/Disclosure) {:as "ul"
                                                   :class "mt-1 px-2"}
                       (for [plugin sidebar-constants/integrations-management]
                         (when (or selfhosted? (not (:selfhosted-only? plugin)))
                           (let [blocked? (and free-license? (not (:free-feature? plugin)))]
                             ^{:key (:name plugin)}
                             [:li
                              [:a {:href (cond
                                           blocked? "#"
                                           (:plugin? plugin) (str "/plugins/manage/" (:name plugin))
                                           :else (:uri plugin))
                                   :on-click (fn [e]
                                               (.preventDefault e)
                                               (if blocked?
                                                 (rf/dispatch [:navigate (:upgrade-plan-route plugin)])
                                                 (if (:plugin? plugin)
                                                   (rf/dispatch [:plugins->navigate->manage-plugin (:name plugin)])
                                                   (rf/dispatch [:navigate (:navigate plugin)])))
                                               (when is-mobile?
                                                 (rf/dispatch [:sidebar-mobile->close])))
                                   :class (str "flex justify-between items-center text-gray-300 hover:text-white hover:bg-white/5 "
                                               "block rounded-md py-2 pr-2 pl-9 text-sm leading-6 "
                                               (when blocked? " text-opacity-30"))}
                               (:label plugin)
                               (when blocked?
                                 [:div {:class styles/badge-upgrade}
                                  "Upgrade"])]])))]]))])

               (when admin?
                 [:> ui/Disclosure {:as "li"
                                    :class "text-xs font-semibold leading-6 text-gray-400"}
                  (fn [params]
                    (r/as-element
                     [:<>
                      [:> (.-Button ui/Disclosure) {:class (str "w-full group flex items-center justify-between rounded-md p-2 text-sm "
                                                                "font-semibold leading-6 text-gray-300 hover:bg-white/5 hover:text-white")
                                                    :aria-label (if (.-open params)
                                                                  "Collapse Settings section"
                                                                  "Expand Settings section")}
                       [:div {:class "flex gap-3 justify-start items-center"}
                        [:> Settings {:size 24
                                      :aria-hidden "true"}]
                        "Settings"]
                       (if (.-open params)
                         [:> ChevronDown {:size 24
                                          :aria-hidden "true"}]
                         [:> ChevronRight {:size 24
                                           :aria-hidden "true"}])]
                      [:> (.-Panel ui/Disclosure) {:as "ul"
                                                   :class "mt-1 px-2"}
                       (for [route sidebar-constants/settings-management]
                         (when (or selfhosted? (not (:selfhosted-only? route)))
                           (let [blocked? (and free-license? (not (:free-feature? route)))]
                             ^{:key (:name route)}
                             [:li
                              [:button {:type "button"
                                        :on-click (fn []
                                                    (if blocked?
                                                      (rf/dispatch [:navigate (:upgrade-plan-route route)])
                                                      (rf/dispatch [:navigate (:navigate route)]))
                                                    (when is-mobile?
                                                      (rf/dispatch [:sidebar-mobile->close])))
                                        :class (str "w-full flex justify-between items-center text-gray-300 hover:text-white hover:bg-white/5 "
                                                    "block rounded-md py-2 pr-2 pl-9 text-sm leading-6 "
                                                    (when blocked? " text-opacity-30"))
                                        :aria-label (:label route)}
                               [:span {:class "flex items-center gap-6"}
                                (:label route)
                                (when (string? (:badge route))
                                  [:> Badge {:variant "solid" :color "green"}
                                   (:badge route)])]
                               (when blocked?
                                 [:div {:class styles/badge-upgrade}
                                  "Upgrade"])]])))]]))])]])

           [:li {:class "mt-auto mb-3"}
            [profile-dropdown {:user-data user-data
                               :auth-method auth-method
                               :gateway-version gateway-version}]]]]]))))
