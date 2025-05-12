(ns webapp.webclient.components.side-panel
  (:require
   ["@radix-ui/themes" :refer [Box Flex Text]]
   ["allotment" :refer [Allotment]]))

;; Componente reutilizável para painéis laterais
(defn side-panel [{:keys [title content]}]
  [:> Box {:class "h-full w-full bg-gray-1 border-l border-gray-3 overflow-y-auto"}
   [:> Flex {:justify "between"
             :align "center"
             :class "px-4 py-3 border-b border-gray-3"}
    [:> Text {:size "3" :weight "bold" :class "text-gray-12"} title]]


   [:> Box {:class "p-4"}
    content]])

;; HOC para adicionar o painel ao layout
(defn with-panel [show-panel? content panel]
  [:> Flex {:class "h-[calc(100%-4rem)]"}
   [:> Allotment {:key (str "allotment-" show-panel?)
                  :defaultSizes [750 250]
                  :horizontal true}
    [:> Box {:class "flex-grow transition-all duration-300"}
     content]
    (when show-panel?
      [side-panel panel])]])
