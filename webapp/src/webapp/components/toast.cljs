(ns webapp.components.toast
  (:require
   ["sonner" :refer [toast]]
   [reagent.core :as r]
   [clojure.string :as str]))

;; Ícones SVG inline (substituir por biblioteca de ícones se preferir)
(defn check-icon []
  [:svg {:width "20" :height "20" :viewBox "0 0 24 24" :fill "none" :stroke "currentColor" :stroke-width "2"}
   [:polyline {:points "20,6 9,17 4,12"}]])

(defn alert-circle-icon []
  [:svg {:width "20" :height "20" :viewBox "0 0 24 24" :fill "none" :stroke "currentColor" :stroke-width "2"}
   [:circle {:cx "12" :cy "12" :r "10"}]
   [:line {:x1 "12" :y1 "8" :x2 "12" :y2 "12"}]
   [:line {:x1 "12" :y1 "16" :x2 "12.01" :y2 "16"}]])

(defn chevron-down-icon []
  [:svg {:width "16" :height "16" :viewBox "0 0 24 24" :fill "none" :stroke "currentColor" :stroke-width "2"}
   [:polyline {:points "6,9 12,15 18,9"}]])

(defn chevron-up-icon []
  [:svg {:width "16" :height "16" :viewBox "0 0 24 24" :fill "none" :stroke "currentColor" :stroke-width "2"}
   [:polyline {:points "18,15 12,9 6,15"}]])

;; Função para formatar JSON de forma legível
(defn format-json [obj]
  (try
    (.stringify js/JSON (clj->js obj) nil 2)
    (catch js/Error _
      (str obj))))

;; Função para obter estilos baseados no tipo
(defn get-toast-styles [toast-type]
  (case toast-type
    :success {:bg "bg-green-50"
              :border "border-green-200"
              :text "text-green-800"
              :icon-color "text-green-600"}
    :error {:bg "bg-red-50"
            :border "border-red-200"
            :text "text-red-800"
            :icon-color "text-red-600"}
    ;; Default
    {:bg "bg-white"
     :border "border-gray-200"
     :text "text-gray-900"
     :icon-color "text-gray-500"}))

;; Toast component principal
(defn toast-component [{:keys [id title description type details button]}]
  (let [expanded? (r/atom false)
        has-details? (and (= type :error) (some? details))
        styles (get-toast-styles type)]

    (fn []
      [:div {:class (str "flex flex-col rounded-lg shadow-lg ring-1 ring-black/5 w-full md:max-w-[364px] p-4 "
                         (:bg styles) " " (:border styles))}

       ;; Header com ícone, título e botão de fechar
       [:div {:class "flex items-start justify-between"}
        [:div {:class "flex items-start gap-3"}
         ;; Ícone baseado no tipo
         [:div {:class (str "flex-shrink-0 " (:icon-color styles))}
          (case type
            :success [check-icon]
            :error [alert-circle-icon]
            [:div])]

         ;; Conteúdo principal
         [:div {:class "flex-1 min-w-0"}
          [:p {:class (str "text-sm font-medium " (:text styles))}
           title]
          (when description
            [:p {:class (str "mt-1 text-sm " (:text styles) " opacity-75")}
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
           (if @expanded? [chevron-up-icon] [chevron-down-icon])
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

;; Função de conveniência para criar toasts simples (mantendo compatibilidade)
(defn toast-simple [message]
  (toast-success message))

;; Exemplo de botão que renderiza diferentes tipos de toast
(defn example-button []
  [:div {:class "flex gap-2 p-4"}
   [:button {:class "px-4 py-2 bg-green-600 text-white rounded hover:bg-green-700"
             :on-click #(toast-success "Plugin updated")}
    "Success Toast"]

   [:button {:class "px-4 py-2 bg-red-600 text-white rounded hover:bg-red-700"
             :on-click #(toast-error "Failed connecting to resource")}
    "Error Toast (simple)"]

   [:button {:class "px-4 py-2 bg-red-600 text-white rounded hover:bg-red-700"
             :on-click #(toast-error
                         "Failed connecting to resource"
                         nil
                         {:message "Failed to connect to remote host=12345, port=5651, reason=dial tcp: address 2351: invalid port"
                          :code "AccessDenied"
                          :type "Sender"})}
    "Error Toast (with details)"]])
