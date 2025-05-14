(ns webapp.shared-ui.sidebar.navigation
  (:require ["@headlessui/react" :as ui]
            ["@heroicons/react/20/solid" :as hero-solid-icon]
            ["@heroicons/react/24/outline" :as hero-outline-icon]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.routes :as routes]
            [webapp.components.user-icon :as user-icon]
            [webapp.config :as config]
            [webapp.components.organization-selector :refer [organization-selector initialize]]
            [webapp.shared-ui.sidebar.constants :as sidebar-constants]))

(def link-styles
  {:enabled "flex justify-between items-center group items-start flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold"
   :disabled (str "flex justify-between items-center text-gray-300 cursor-not-allowed text-opacity-30 "
                  "group items-start flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold")})

(defn hover-side-menu-link? [uri-item current-route]
  (if (= uri-item current-route)
    "bg-white/5 text-white "
    "hover:bg-white/5 hover:text-white text-gray-300 "))

(defn main [_ _]
  (let [open-profile-disclosure? (r/atom false)
        gateway-info (rf/subscribe [:gateway->info])
        current-route (rf/subscribe [:routes->route])]
    ;; Initialize organization selector
    (initialize)
    (fn [user my-plugins]
      (let [gateway-version (:version (:data @gateway-info))
            auth-method (:auth_method (:data @gateway-info))
            user-data (:data user)
            plugins-routes-enabled (filterv (fn [plugin]
                                              (some #(= (:name plugin) (:name %)) my-plugins))
                                            sidebar-constants/plugins-routes)
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
               [:li {:class (str (when
                                  (and (:admin-only? route) (not admin?)) "hidden"))}
                [:a {:href (if (and free-license? (not (:free-feature? route)))
                             "#"
                             (:uri route))
                     :on-click (fn []
                                 (when (and free-license? (not (:free-feature? route)))
                                   (rf/dispatch [:navigate :upgrade-plan])))
                     :class (str (hover-side-menu-link? (:uri route) current-route)
                                 (:enabled link-styles)
                                 (when (and free-license? (not (:free-feature? route)))
                                   " text-opacity-30"))}
                 [:div {:class "flex gap-3 items-center"}
                  [(:icon route) {:class (str "h-6 w-6 shrink-0 text-white"
                                              (when (and free-license? (not (:free-feature? route)))
                                                " opacity-30"))
                                  :aria-hidden "true"}]
                  (:name route)]
                 (when (and free-license? (not (:free-feature? route)))
                   [:div {:class "text-xs text-gray-200 py-1 px-2 border border-gray-200 rounded-md"}
                    "Upgrade"])]])

             (for [plugin plugins-routes-enabled]
               ^{:key (:name plugin)}
               [:li
                [:a {:href (if (and free-license? (not (:free-feature? plugin)))
                             "#"
                             (:uri plugin))
                     :on-click (fn []
                                 (when (and free-license? (not (:free-feature? plugin)))
                                   (rf/dispatch [:navigate :upgrade-plan])))
                     :class (str (hover-side-menu-link? (:uri plugin) current-route)
                                 (:enabled link-styles)
                                 (when (and free-license? (not (:free-feature? plugin)))
                                   " text-opacity-30"))}
                 [:div {:class "flex gap-3 items-center"}
                  [(:icon plugin) {:class (str "h-6 w-6 shrink-0 text-white"
                                               (when (and free-license? (not (:free-feature? plugin)))
                                                 " opacity-30"))
                                   :aria-hidden "true"}]
                  (:label plugin)]
                 (when (and free-license? (not (:free-feature? plugin)))
                   [:div {:class "text-xs text-gray-200 py-1 px-2 border border-gray-200 rounded-md"}
                    "Upgrade"])]])]]

           [:ul {:class "space-y-1"}
            [:div {:class "py-0.5 text-xs text-white mb-3 font-semibold"}
             "Organization"]
            
            ;; Organization selector component
            [organization-selector]

            [:li
             [:a {:href "#"
                  :on-click #(rf/dispatch [:navigate :connections])
                  :class (str (hover-side-menu-link? "/connections" current-route)
                              (:enabled link-styles))}
              [:div {:class "flex gap-3 items-center"}
               [:> hero-outline-icon/ArrowsRightLeftIcon {:class "h-6 w-6 shrink-0 text-white"
                                                          :aria-hidden "true"}]
               "Connections"]]]

            (when admin?
              [:<>
               [:li
                [:a {:href "#"
                     :on-click #(rf/dispatch [:navigate :users])
                     :class (str (hover-side-menu-link? "/organization/users" current-route)
                                 (:enabled link-styles))}
                 [:div {:class "flex gap-3 items-center"}
                  [:> hero-outline-icon/UserGroupIcon {:class "h-6 w-6 shrink-0 text-white"
                                                       :aria-hidden "true"}]
                  "Users"]]]

               [:li
                [:a {:href "#"
                     :on-click #(rf/dispatch [:navigate :guardrails])
                     :class (str (hover-side-menu-link? "/guardrails" current-route)
                                 (:enabled link-styles))}
                 [:div {:class "flex gap-3 items-center"}
                  [:> hero-outline-icon/ShieldCheckIcon {:class "h-6 w-6 shrink-0 text-white"
                                                         :aria-hidden "true"}]
                  "Guardrails"]]]
               [:li
                [:a {:href (routes/url-for :agents)
                     :class (str (hover-side-menu-link? "/agents" current-route)
                                 (:enabled link-styles))}
                 [:div {:class "flex gap-3 items-center"}
                  [:> hero-outline-icon/ServerStackIcon {:class "h-6 w-6 shrink-0 text-white"
                                                         :aria-hidden "true"}]
                  "Agents"]]]

               [:li
                [:a {:href "#"
                     :on-click #(rf/dispatch [:navigate :jira-templates])
                     :class (str (hover-side-menu-link? "/jira-templates" current-route)
                                 (:enabled link-styles))}
                 [:div {:class "flex gap-3 items-center"}
                  [:div
                   [:figure {:class "flex-shrink-0 w-6"}
                    [:img {:src (str config/webapp-url "/icons/icon-jira.svg")}]]]
                  "Jira Templates"]]]])

            (when admin?
              [:> ui/Disclosure {:as "li"
                                 :class "text-xs font-semibold leading-6 text-gray-400"}
               [:> (.-Button ui/Disclosure) {:class "w-full group flex items-center justify-between rounded-md p-2 text-sm font-semibold leading-6 text-gray-300 hover:bg-white/5 hover:text-white"}
                [:div {:class "flex gap-3 justify-start items-center"}
                 [:> hero-outline-icon/Cog8ToothIcon {:class "h-6 w-6 shrink-0 text-white"
                                                      :aria-hidden "true"}]
                 "Settings"]
                [:> hero-solid-icon/ChevronDownIcon {:class "text-white h-5 w-5 shrink-0"
                                                     :aria-hidden "true"}]]
               [:> (.-Panel ui/Disclosure) {:as "ul"
                                            :class "mt-1 px-2"}
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
                    [:div {:class "text-xs text-gray-200 py-1 px-2 border border-gray-200 rounded-md"}
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
                  "License"]]]])

            (when admin?
              [:> ui/Disclosure {:as "li"
                                 :class "text-xs font-semibold leading-6 text-gray-400"}
               [:> (.-Button ui/Disclosure) {:class "w-full group flex items-center justify-between rounded-md p-2 text-sm font-semibold leading-6 text-gray-300 hover:bg-white/5 hover:text-white"}
                [:div {:class "flex gap-3 justify-start items-center"}
                 [:> hero-outline-icon/PuzzlePieceIcon {:class "h-6 w-6 shrink-0 text-white"
                                                        :aria-hidden "true"}]
                 "Integrations"]
                [:> hero-solid-icon/ChevronDownIcon {:class "text-white h-5 w-5 shrink-0"
                                                     :aria-hidden "true"}]]
               [:> (.-Panel ui/Disclosure) {:as "ul"
                                            :class "mt-1 px-2"}
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
                      [:div {:class "text-xs text-gray-200 py-1 px-2 border border-gray-200 rounded-md"}
                       "Upgrade"])]])
                [:li
                 [:a
                  {:href (routes/url-for :integrations-aws-connect)
                   :class (str "flex justify-between items-center text-gray-300 hover:text-white hover:bg-white/5 "
                               "block rounded-md py-2 pr-2 pl-9 text-sm leading-6")}
                  "AWS Connect"]]]])]

           [:li {:class "mt-auto mb-3"}
            [:> ui/Disclosure {:as "div"
                               :class "text-xs font-semibold leading-6 text-gray-400"}
             [:> (.-Button ui/Disclosure) {:class "w-full group flex justify-between items-center rounded-md p-2 text-sm font-semibold leading-6 text-gray-300 hover:bg-white/5 hover:text-white"
                                           :onClick #(reset! open-profile-disclosure? (not @open-profile-disclosure?))}
              [:div {:class "flex gap-3 justify-start items-center"}
               [user-icon/initials-white (:name user-data)]
               (subs (:name user-data) 0 (min (count (:name user-data)) 16))]
              [:> hero-solid-icon/ChevronDownIcon {:class "text-white h-5 w-5 shrink-0"
                                                   :aria-hidden "true"}]]
             [:> (.-Panel ui/Disclosure) {:as "ul"
                                          :class "mt-1 px-2"}
              [:li
               [:a {:target "_blank"
                    :href "https://hoop.canny.io/"
                    :class "group -mx-2 flex items-center gap-x-3 rounded-md p-2 text-sm font-semibold leading-6 text-gray-300 hover:bg-white/5 hover:text-white"}
                [:> hero-outline-icon/SparklesIcon {:class "h-6 w-6 shrink-0 text-white"
                                                    :aria-hidden "true"}]
                "Feature request"]]
              [:li
               [:a {:target "_blank"
                    :href "https://help.hoop.dev"
                    :class "group -mx-2 flex items-center gap-x-3 rounded-md p-2 text-sm font-semibold leading-6 text-gray-300 hover:bg-white/5 hover:text-white"}
                [:> hero-outline-icon/ChatBubbleLeftEllipsisIcon {:class "h-6 w-6 shrink-0 text-white"
                                                                  :aria-hidden "true"}]
                "Contact support"]]
              [:li
               [:a {:onClick #(rf/dispatch [:auth->logout {:idp? (= auth-method "idp")}])
                    :href "#"
                    :class "group -mx-2 flex items-center gap-x-3 rounded-md p-2 text-sm font-semibold leading-6 text-gray-300 hover:bg-white/5 hover:text-white"}
                [:> hero-outline-icon/ArrowLeftOnRectangleIcon {:class "h-6 w-6 shrink-0 text-white"
                                                                :aria-hidden "true"}]
                "Log out"]]
              [:li {:class "flex flex-col gap-2 mt-3 opacity-20"}
               [:span {:class "text-xxs text-gray-200 block"}
                (str "webapp version " config/app-version)]
               [:span {:class  "text-xxs text-gray-200"}
                (str "gateway version " gateway-version)]]]]]]]]))))
