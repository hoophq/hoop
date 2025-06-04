(ns webapp.shared-ui.sidebar.components.section
  (:require ["@headlessui/react" :as ui]
            ["@heroicons/react/20/solid" :as hero-solid-icon]
            [react :as react]))

(defn section-title
  "Título da seção da sidebar"
  [title]
  [:div {:class "py-0.5 text-xs text-white mb-3 font-semibold"}
   title])

(defn disclosure-section
  "Seção expansível (dropdown) da sidebar"
  [{:keys [title icon children]}]
  [:> ui/Disclosure {:as "li"
                     :class "text-xs font-semibold leading-6 text-gray-400"}
   [:> (.-Button ui/Disclosure) {:class "w-full group flex items-center justify-between rounded-md p-2 text-sm font-semibold leading-6 text-gray-300 hover:bg-white/5 hover:text-white"}
    [:div {:class "flex gap-3 justify-start items-center"}
     [icon {:class "h-6 w-6 shrink-0 text-white"
            :aria-hidden "true"}]
     title]
    [:> hero-solid-icon/ChevronDownIcon {:class "text-white h-5 w-5 shrink-0"
                                         :aria-hidden "true"}]]
   [:> (.-Panel ui/Disclosure) {:as "ul"
                                :class "mt-1 px-2"}
    children]])
