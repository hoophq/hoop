(ns webapp.shared-ui.sidebar.components.profile
  (:require
   ["@headlessui/react" :as ui]
   ["@radix-ui/themes" :refer [Text]]
   ["lucide-react" :refer [ChevronDown ChevronRight LogOut
                           MessageCircleQuestion MessageSquarePlus]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.user-icon :as user-icon]
   [webapp.config :as config]))

(defn profile-dropdown
  "Dropdown de perfil do usuário na sidebar"
  [{:keys [user-data auth-method gateway-version]}]
  [:> ui/Disclosure {:as "div"
                     :class "text-xs font-semibold leading-6 text-gray-400"}
   (fn [params]
     (r/as-element
      [:<>
       [:> (.-Button ui/Disclosure) {:class "w-full group flex justify-between items-center rounded-md p-2 text-sm font-semibold leading-6 text-gray-300 hover:bg-white/5 hover:text-white"
                                     :aria-label "Open user menu"}
        [:div {:class "flex gap-3 justify-start items-center"}
         [user-icon/initials-white (:name user-data)]
         (subs (:name user-data) 0 (min (count (:name user-data)) 16))]
        (if (.-open params)
          [:> ChevronDown {:size 24
                           :aria-hidden "true"}]
          [:> ChevronRight {:size 24
                            :aria-hidden "true"}])]
       [:> (.-Panel ui/Disclosure) {:as "ul"
                                    :class "mt-1 px-2"}
        [:li
         [:a {:target "_blank"
              :href "https://github.com/hoophq/hoop/issues"
              :rel "noopener noreferrer"
              :class "group -mx-2 flex items-center gap-x-3 rounded-md p-2 text-sm font-semibold leading-6 text-gray-300 hover:bg-white/5 hover:text-white"}
          [:> MessageSquarePlus {:size 24
                                 :aria-hidden "true"}]
          "Feature request"]]
        [:li
         [:button {:id "intercom-support-trigger"
                   :onClick (fn []
                              (let [analytics-tracking @(rf/subscribe [:gateway->analytics-tracking])]
                                (when-not analytics-tracking
                                  (.open js/window "https://github.com/hoophq/hoop/discussions" "_blank"))))
                   :class "w-full group -mx-2 flex items-center gap-x-3 rounded-md p-2 text-sm font-semibold leading-6 text-gray-300 hover:bg-white/5 hover:text-white"}
          [:> MessageCircleQuestion {:size 24
                                     :aria-hidden "true"}]
          "Contact support"]]
        [:li
         [:button {:onClick #(rf/dispatch [:auth->logout {:idp? (= auth-method "idp")}])
                   :class "w-full group -mx-2 flex items-center gap-x-3 rounded-md p-2 text-sm font-semibold leading-6 text-gray-300 hover:bg-white/5 hover:text-white"}
          [:> LogOut {:size 24
                      :aria-hidden "true"}]
          "Log out"]]
        [:li {:class "mt-3"}
         [:> Text {:size "1" :weight "light" :class "light text-gray-7"}
          (str "Gateway Version " gateway-version)]]]]))])
