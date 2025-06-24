(ns webapp.components.toast
  (:require
   ["sonner" :refer [toast]]
   [reagent.core :as r]))

;; Toast totalmente customizado que mantém animações e interações
(defn toast-component [{:keys [id title description button]}]
  [:div {:class "flex rounded-lg bg-white shadow-lg ring-1 ring-black/5 w-full md:max-w-[364px] items-center p-4"}
   [:div {:class "flex flex-1 items-center"}
    [:div {:class "w-full"}
     [:p {:class "text-sm font-medium text-gray-900"} title]
     [:p {:class "mt-1 text-sm text-gray-500"} description]]]

   (when button
     [:div {:class "ml-5 shrink-0 rounded-md text-sm font-medium text-indigo-600 hover:text-indigo-500 focus:ring-2 focus:ring-indigo-500 focus:ring-offset-2 focus:outline-hidden"}
      [:button {:class "rounded bg-indigo-50 px-3 py-1 text-sm font-semibold text-indigo-600 hover:bg-indigo-100"
                :on-click (fn []
                            (when (:on-click button)
                              ((:on-click button)))
                            (toast.dismiss id))}
       (:label button)]])])

;; Função toast customizada abstrata (equivalente ao toast() do React)
;; Recomento abstrair a função toast para não precisar usar toast.custom toda vez
(defn custom-toast [toast-data]
  (toast.custom
   (fn [id]
     (r/as-element
      [toast-component {:id id
                        :title (:title toast-data)
                        :description (:description toast-data)
                        :button (:button toast-data)}]))))

;; Função de conveniência para criar toasts simples
(defn toast-simple [message]
  (custom-toast {:title message
                 :description ""}))

;; Exemplo de botão que renderiza toast (pode ser removido depois)
(defn example-button []
  [:button {:class "relative flex h-10 flex-shrink-0 items-center justify-center gap-2 overflow-hidden rounded-full bg-white px-4 text-sm font-medium shadow-sm transition-all hover:bg-[#FAFAFA] dark:bg-[#161615] dark:hover:bg-[#1A1A19] dark:text-white"
            :on-click #(custom-toast {:title "This is a headless toast"
                                      :description "You have full control of styles and jsx, while still having the animations."
                                      :button {:label "Reply"
                                               :on-click (fn [] (toast.dismiss))}})}
   "Render toast"])
