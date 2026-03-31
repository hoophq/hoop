(ns webapp.features.machine-identities.views.empty-state
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   [re-frame.core :as rf]))

(defn main []
  [:> Box {:class "flex flex-col h-full items-center justify-between py-16 px-4 max-w-3xl mx-auto"}
   [:> Flex {:direction "column" :align "center"}
    [:> Box {:class "mb-8 w-80"}
     ;; TODO: Adicionar imagem em /public/images/illustrations/machine-identities-empty.png
     ;; Por enquanto, placeholder visual com ícone
     [:div {:class "flex items-center justify-center h-64 bg-gray-100 rounded-lg"}
      [:div {:class "text-center text-gray-400"}
       [:div {:class "text-6xl mb-4"} "🔐"]
       [:div {:class "text-sm"} "Image placeholder"]]]]

    [:> Flex {:direction "column" :align "center" :gap "3" :class "mb-8 text-center"}
     [:> Text {:size "3" :class "text-gray-11 max-w-md text-center"}
      "No Machine Identities configured in your Organization yet"]]

    [:> Button {:size "3"
                :onClick #(rf/dispatch [:navigate :machine-identities-new])}
     "Create new identity"]]

   [:> Flex {:align "center" :class "text-sm mt-24"}
    [:> Text {:class "text-gray-11 mr-1"}
     "Need more information? Check out"]
    [:a {:href "#"
         :target "_blank"
         :class "text-blue-600 hover:underline"}
     "Machine Identities documentation."]]])
