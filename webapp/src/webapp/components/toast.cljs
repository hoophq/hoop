(ns webapp.components.toast
  (:require
   ["@radix-ui/themes" :refer [Text]]
   ["lucide-react" :refer [CheckCircle AlertCircle ChevronDown ChevronUp]]
   ["sonner" :refer [toast]]
   [reagent.core :as r]))


;; Função para formatar JSON de forma legível
(defn format-json [obj]
  (try
    (.stringify js/JSON (clj->js obj) nil 2)
    (catch js/Error _
      (str obj))))

;; Função para obter estilos baseados no tipo
(defn get-toast-styles [toast-type]
  (case toast-type
    :success {:icon-color "text-green-600"}
    :error {:icon-color "text-red-600"}
    ;; Default
    {:icon-color "text-gray-500"}))

;; Toast component principal
(defn toast-component [{:keys [id title description type details button]}]
  (let [expanded? (r/atom false)
        has-details? (and (= type :error) (some? details))
        styles (get-toast-styles type)]

    (fn []
      [:div {:class (str "flex flex-col rounded-lg shadow-lg ring-1 "
                         "ring-black/5 w-full w-[364px] p-4 bg-white")}

       ;; Header com ícone, título e botão de fechar
       [:div {:class "flex items-start justify-between"}
        [:div {:class "flex items-start gap-3"}
         ;; Ícone baseado no tipo
         [:div {:class (str "flex-shrink-0 " (:icon-color styles))}
          (case type
            :success [:> CheckCircle {:size "20"}]
            :error [:> AlertCircle {:size "20"}]
            [:div])]

         ;; Conteúdo principal
         [:div {:class "flex-1 min-w-0"}
          [:> Text {:as "p" :size "2" :class "text-gray-12"}
           title]
          (when description
            [:> Text {:as "p" :size "2" :class "text-gray-12"}
             description])]]

        ;; Botão de fechar
        [:button {:class "flex-shrink-0 text-gray-400 hover:text-gray-600 transition-colors"
                  :on-click #(toast.dismiss id)}
         [:svg {:width "16" :height "16" :viewBox "0 0 24 24" :fill "none" :stroke "currentColor" :stroke-width "2"}
          [:line {:x1 "18" :y1 "6" :x2 "6" :y2 "18"}]
          [:line {:x1 "6" :y1 "6" :x2 "18" :y2 "18"}]]]]

       ;; Botão "Show/Hide details" (apenas para erros com detalhes)
       (when has-details?
         [:div {:class "mt-3 pt-3 border-t border-gray-200"}
          [:button {:class "flex items-center gap-2 text-sm text-gray-600 hover:text-gray-800 transition-colors"
                    :on-click #(swap! expanded? not)}
           (if @expanded? [:> ChevronUp {:size "20"}] [:> ChevronDown {:size "20"}])
           (if @expanded? "Hide details" "Show details")]])

       ;; Área expansível com detalhes do erro
       (when (and has-details? @expanded?)
         [:div {:class "mt-3 p-3 bg-gray-100 rounded-md border border-gray-200"}
          [:pre {:class "text-xs text-gray-700 whitespace-pre-wrap overflow-x-auto"}
           (format-json details)]])

       ;; Botão de ação customizado (se fornecido)
       (when button
         [:div {:class "mt-3 pt-3 border-t border-gray-200 flex justify-end"}
          [:button {:class "px-3 py-1 text-sm font-semibold text-indigo-600 bg-indigo-50 hover:bg-indigo-100 rounded transition-colors"
                    :on-click (fn []
                                (when (:on-click button)
                                  ((:on-click button)))
                                (toast.dismiss id))}
           (:label button)]])])))

;; Função toast customizada
(defn custom-toast [{:keys [type title description details button] :as toast-data}]
  (toast.custom
   (fn [id]
     (r/as-element
      [toast-component (assoc toast-data :id id)]))))

;; Funções helper para diferentes tipos
(defn toast-success
  ([title] (toast-success title nil))
  ([title description]
   (custom-toast {:type :success
                  :title title
                  :description description})))

(defn toast-error
  ([title] (toast-error title nil nil))
  ([title description] (toast-error title description nil))
  ([title description details]
   (custom-toast {:type :error
                  :title title
                  :description description
                  :details details})))
