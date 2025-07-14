(ns webapp.components.selection-card
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Card Flex Text]]))

(defn selection-card [{:keys [icon title description selected? on-click badge]}]
  (println "selected?" selected?)
  [:> Card {:size "1"
            :variant "surface"
            :class (str "w-full cursor-pointer "
                        (when selected? "before:bg-primary-12"))
            :on-click on-click}
   [:> Flex {:align "center" :gap "3" :class (str (when selected? "text-[--gray-1]"))}
    (when icon
      [:> Avatar {:size "4"
                  :class (when selected? "dark")
                  :variant "soft"
                  :color "gray"
                  :fallback icon}])
    [:> Flex {:direction "column"}
     [:> Flex {:align "center" :gap "2"}
      [:> Text {:size "3" :weight "medium" :color "gray-12"} title]
      (when badge
        [:> Box {:class "text-xs font-medium px-2 py-0.5 rounded-full bg-success-9 text-white"}
         badge])]
     (when description
       [:> Text {:size "2" :color "gray-11"} description])]]])
