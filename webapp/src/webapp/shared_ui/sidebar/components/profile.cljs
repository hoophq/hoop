(ns webapp.shared-ui.sidebar.components.profile
  (:require ["@headlessui/react" :as ui]
            ["@heroicons/react/20/solid" :as hero-solid-icon]
            ["@heroicons/react/24/outline" :as hero-outline-icon]
            [re-frame.core :as rf]
            [webapp.components.user-icon :as user-icon]
            [webapp.config :as config]))

(defn profile-dropdown
  "Dropdown de perfil do usuário na sidebar"
  [{:keys [user-data auth-method gateway-version]}]
  [:> ui/Disclosure {:as "div"
                     :class "text-xs font-semibold leading-6 text-gray-400"}
   [:> (.-Button ui/Disclosure) {:class "w-full group flex justify-between items-center rounded-md p-2 text-sm font-semibold leading-6 text-gray-300 hover:bg-white/5 hover:text-white"}
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
      (str "gateway version " gateway-version)]]]])
