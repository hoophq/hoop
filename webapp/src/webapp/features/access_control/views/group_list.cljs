(ns webapp.features.access-control.views.group-list
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Card Text Badge]]
   [re-frame.core :as rf]
   [webapp.components.headings :as h]))

;; Placeholder para lista de grupos - será implementado completamente na próxima iteração
(defn main []
  (let [user-groups (rf/subscribe [:user-groups])
        plugin-details (rf/subscribe [:plugins->plugin-details])]
    (fn []
      [:> Box {:class "w-full"}
       [:> Flex {:justify "between" :align "center" :class "mb-6"}
        [:> Text {:size "5" :weight "bold"} "User Groups"]
        [:> Button {:size "3"
                    :class "bg-blue-600 hover:bg-blue-700"
                    :onClick #(rf/dispatch [:navigate :access-control-new])}
         "Create Group"]]

       ;; Placeholder para a lista de grupos
       [:> Box {:class "space-y-4"}
        [:> Text {:size "3" :class "italic text-gray-11 text-center py-8"}
         "Group list will be implemented in the next iteration..."]]])))
