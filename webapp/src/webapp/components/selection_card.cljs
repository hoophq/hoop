(ns webapp.components.selection-card
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Card Flex Text]]
   ["lucide-react" :refer [Check]]
   [reagent.core :as r]))

(defn selection-card [{:keys [icon title description selected? on-click badge]}]
  [:> Card {:size "1"
            :variant "surface"
            :class (str "w-full cursor-pointer group transition-all "
                        (if selected?
                          "hover:before:bg-primary-12 ring-2 ring-blue-500"
                          "hover:before:bg-primary-12"))
            :on-click on-click}
   [:> Flex {:align "center" :gap "3" :class "group-hover:text-[--gray-1]"}
    ;; Radio indicator
    [:> Box {:class (str "w-5 h-5 rounded-full border-2 flex items-center justify-center flex-shrink-0 "
                         (if selected?
                           "border-blue-500 bg-blue-500"
                           "border-gray-300 group-hover:border-gray-400"))}
     (when selected?
       [:> Check {:size 12 :class "text-white"}])]
    ;; Icon
    (when icon
      [:> Avatar {:size "4"
                  :class (str "group-hover:bg-[--white-a3] flex-shrink-0 "
                              (when selected? "bg-blue-100"))
                  :variant "soft"
                  :color (if selected? "blue" "gray")
                  :fallback icon}])
    ;; Content
    [:> Flex {:direction "column" :class "flex-1"}
     [:> Flex {:align "center" :gap "2"}
      [:> Text {:size "3" :weight "medium" :color "gray-12"} title]
      (when badge
        [:> Box {:class "text-xs font-medium px-2 py-0.5 rounded-full bg-success-9 text-white"}
         badge])]
     [:> Text {:size "2" :color "gray-11"} description]]]])
