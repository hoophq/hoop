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
       [:> (.-Button ui/Disclosure) {:class "w-full group flex justify-between items-center rounded-md p-2 text-sm font-semibold leading-6 text-gray-300 hover:bg-white/5 hover:text-white"}
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
              :href "https://hoop.canny.io/"
              :class "group -mx-2 flex items-center gap-x-3 rounded-md p-2 text-sm font-semibold leading-6 text-gray-300 hover:bg-white/5 hover:text-white"}
          [:> MessageSquarePlus {:size 24
                                 :aria-hidden "true"}]
          "Feature request"]]
        [:li
         [:a {:target "_blank"
              :href "https://help.hoop.dev"
              :class "group -mx-2 flex items-center gap-x-3 rounded-md p-2 text-sm font-semibold leading-6 text-gray-300 hover:bg-white/5 hover:text-white"}
          [:> MessageCircleQuestion {:size 24
                                     :aria-hidden "true"}]
          "Contact support"]]
        [:li
         [:a {:onClick #(rf/dispatch [:auth->logout {:idp? (= auth-method "idp")}])
              :href "#"
              :class "group -mx-2 flex items-center gap-x-3 rounded-md p-2 text-sm font-semibold leading-6 text-gray-300 hover:bg-white/5 hover:text-white"}
          [:> LogOut {:size 24
                      :aria-hidden "true"}]
          "Log out"]]
        [:li {:class "flex flex-col mt-3 gap-1"}
         [:> Text {:size "1" :class "text-gray-7"}
          (str "Webapp Version " config/app-version)]
         [:> Text {:size "1" :class "text-gray-7"}
          (str "Gateway Version " gateway-version)]]]]))])
