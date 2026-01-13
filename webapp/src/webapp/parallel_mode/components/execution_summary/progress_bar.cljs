(ns webapp.parallel-mode.components.execution-summary.progress-bar
  (:require
   ["@radix-ui/themes" :refer [Box Flex Progress Text]]
   ["lucide-react" :refer [Info]]
   [re-frame.core :as rf]))

(defn main []
  (let [progress-data (rf/subscribe [:parallel-mode/execution-progress])
        fade-out? (rf/subscribe [:parallel-mode/should-fade-out?])]
    (fn []
      (let [{:keys [total running percentage]} @progress-data]
        [:> Box {:class (str "px-6 py-4 border-b border-gray-6 bg-gray-2 transition-all duration-500 "
                             (when @fade-out? "opacity-0 h-0 overflow-hidden"))}
         [:> Flex {:direction "column" :gap "3"}
          [:> Flex {:justify "between" :align "center"}
           [:> Text {:size "2" :weight "medium" :class "text-gray-12"}
            (str "Running " running " of " total)]
           [:> Flex {:align "center" :gap "2"}
            [:> Info {:size 14 :class "text-gray-11"}]
            [:> Text {:size "1" :color "gray"}
             "Keep this screen open while running"]]]

          [:> Progress
           {:value percentage
            :max 100
            :size "3"
            :class "w-full"}]]]))))

