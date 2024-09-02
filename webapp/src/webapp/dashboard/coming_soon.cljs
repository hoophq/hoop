(ns webapp.dashboard.coming-soon
  (:require ["@radix-ui/themes" :refer [Flex Heading Text]]
            ["@heroicons/react/24/solid" :as hero-solid-icon]))

(defn main []
  [:> Flex {:flexGrow "1" :direction "column" :align "center" :justify "center"}
   [:> hero-solid-icon/ChartBarIcon {:class "w-8 h-8 text-gray-900"}]
   [:> Heading {:as "h3" :size "2"}
    "Coming soon"]
   [:> Text {:as "label" :color "gray" :weight "light" :size "1"}
    "Stay tuned for more insights"]])
