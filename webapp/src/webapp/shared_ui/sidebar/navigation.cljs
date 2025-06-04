(ns webapp.shared-ui.sidebar.navigation
  (:require ["@heroicons/react/24/outline" :as hero-outline-icon]
            [re-frame.core :as rf]
            [webapp.routes :as routes]
            [webapp.config :as config]
            [webapp.shared-ui.sidebar.constants :as sidebar-constants]
            [webapp.shared-ui.sidebar.styles :as styles]
            [webapp.shared-ui.sidebar.components.nav-link :refer [nav-link]]
            [webapp.shared-ui.sidebar.components.section :refer [section-title disclosure-section]]
            [webapp.shared-ui.sidebar.components.profile :refer [profile-dropdown]]))

(defn main [_ _]
  (let [gateway-info (rf/subscribe [:gateway->info])
        current-route (rf/subscribe [:routes->route])]
    (fn [user my-plugins]
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
                :class "flex flex-1 flex-col gap-y-6"}
           [:li
            [:ul {:role "list" :class "space-y-1"}
             (for [route sidebar-constants/routes]
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

           [:ul {:class "space-y-1"}
            [section-title "Organization"]

            [:li
             [:a {:href "#"
                  :on-click #(rf/dispatch [:navigate :connections])
                  :class (str (styles/hover-side-menu-link "/connections" current-route)
                              (:enabled styles/link-styles))}
              [:div {:class "flex gap-3 items-center"}
               [:> hero-outline-icon/ArrowsRightLeftIcon {:class "h-6 w-6 shrink-0 text-white"
                                                          :aria-hidden "true"}]
               "Connections"]]]

            (when admin?
              [:<>
               [:li
                [:a {:href "#"
                     :on-click #(rf/dispatch [:navigate :users])
                     :class (str (styles/hover-side-menu-link "/organization/users" current-route)
                                 (:enabled styles/link-styles))}
                 [:div {:class "flex gap-3 items-center"}
                  [:> hero-outline-icon/UserGroupIcon {:class "h-6 w-6 shrink-0 text-white"
                                                       :aria-hidden "true"}]
                  "Users"]]]

               [:li
                [:a {:href "#"
                     :on-click #(rf/dispatch [:navigate :guardrails])
                     :class (str (styles/hover-side-menu-link "/guardrails" current-route)
                                 (:enabled styles/link-styles))}
                 [:div {:class "flex gap-3 items-center"}
                  [:> hero-outline-icon/ShieldCheckIcon {:class "h-6 w-6 shrink-0 text-white"
                                                         :aria-hidden "true"}]
                  "Guardrails"]]]
               [:li
                [:a {:href (routes/url-for :agents)
                     :class (str (styles/hover-side-menu-link "/agents" current-route)
                                 (:enabled styles/link-styles))}
                 [:div {:class "flex gap-3 items-center"}
                  [:> hero-outline-icon/ServerStackIcon {:class "h-6 w-6 shrink-0 text-white"
                                                         :aria-hidden "true"}]
                  "Agents"]]]

               [:li
                [:a {:href "#"
                     :on-click #(rf/dispatch [:navigate :jira-templates])
                     :class (str (styles/hover-side-menu-link "/jira-templates" current-route)
                                 (:enabled styles/link-styles))}
                 [:div {:class "flex gap-3 items-center"}
                  [:div
                   [:figure {:class "flex-shrink-0 w-6"}
                    [:img {:src (str config/webapp-url "/icons/icon-jira.svg")}]]]
                  "Jira Templates"]]]])

            (when admin?
              [disclosure-section
               {:title "Settings"
                :icon (fn [props]
                        [:> hero-outline-icon/Cog8ToothIcon props])
                :children
                [:<>
                 [:li
                  [:a
                   {:on-click (fn []
                                (if free-license?
                                  (rf/dispatch [:navigate :upgrade-plan])
                                  (rf/dispatch [:navigate :manage-ask-ai])))
                    :href "#"
                    :class (str "flex justify-between items-center text-gray-300 hover:text-white hover:bg-white/5 "
                                "block rounded-md py-2 pr-2 pl-9 text-sm leading-6"
                                (when free-license?
                                  " text-opacity-30"))}
                   "AI Query Builder"
                   (when free-license?
                     [:div {:class styles/badge-upgrade}
                      "Upgrade"])]]

                 [:li
                  [:a
                   {:href (routes/url-for :access-control)
                    :class (str "flex justify-between items-center text-gray-300 hover:text-white hover:bg-white/5 "
                                "block rounded-md py-2 pr-2 pl-9 text-sm leading-6")}
                   "Access Control"]]

                 [:li
                  [:a
                   {:href (routes/url-for :runbooks)
                    :class (str "flex justify-between items-center text-gray-300 hover:text-white hover:bg-white/5 "
                                "block rounded-md py-2 pr-2 pl-9 text-sm leading-6")}
                   "Runbooks"]]

                 [:li
                  [:a
                   {:href (routes/url-for :license-management)
                    :class (str "flex justify-between items-center text-gray-300 hover:text-white hover:bg-white/5 "
                                "block rounded-md py-2 pr-2 pl-9 text-sm leading-6")}
                   "License"]]]}])

            (when admin?
              [disclosure-section {:title "Integrations"
                                   :icon (fn [props]
                                           [:> hero-outline-icon/PuzzlePieceIcon props])
                                   :children
                                   [:<>
                                    (for [plugin sidebar-constants/integrations-management]
                                      ^{:key (:name plugin)}
                                      [:li
                                       [:a {:on-click (fn []
                                                        (if (and free-license? (not (:free-feature? plugin)))
                                                          (rf/dispatch [:navigate :upgrade-plan])
                                                          (rf/dispatch [:plugins->navigate->manage-plugin (:name plugin)])))
                                            :href "#"
                                            :class (str "flex justify-between items-center text-gray-300 hover:text-white hover:bg-white/5 "
                                                        "block rounded-md py-2 pr-2 pl-9 text-sm leading-6"
                                                        (when (and free-license? (not (:free-feature? plugin)))
                                                          " text-opacity-30"))}
                                        (:label plugin)
                                        (when (and free-license? (not (:free-feature? plugin)))
                                          [:div {:class styles/badge-upgrade}
                                           "Upgrade"])]])
                                    [:li
                                     [:a
                                      {:href (routes/url-for :integrations-aws-connect)
                                       :class (str "flex justify-between items-center text-gray-300 hover:text-white hover:bg-white/5 "
                                                   "block rounded-md py-2 pr-2 pl-9 text-sm leading-6")}
                                      "AWS Connect"]]]}])]]

          [:li {:class "mt-auto mb-3"}
           [profile-dropdown {:user-data user-data
                              :auth-method auth-method
                              :gateway-version gateway-version}]]]]))))
