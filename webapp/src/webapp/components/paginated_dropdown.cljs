(ns webapp.components.paginated-dropdown
  (:require
   ["@radix-ui/themes" :refer [Flex Box Text]]
   [reagent.core :as r]
   [re-frame.core :as rf]))

(defn- search-icon []
  [:svg.h-5.w-5.text-gray-400
   {:xmlns "http://www.w3.org/2000/svg"
    :viewBox "0 0 20 20"
    :fill "currentColor"}
   [:path
    {:fill-rule "evenodd"
     :d "M9 3.5a5.5 5.5 0 100 11 5.5 5.5 0 000-11zM2 9a7 7 0 1112.452 4.391l3.328 3.329a.75.75 0 11-1.06 1.06l-3.329-3.328A7 7 0 012 9z"
     :clip-rule "evenodd"}]])

(defn- chevron-icon [open?]
  [:svg.h-5.w-5
   {:xmlns "http://www.w3.org/2000/svg"
    :viewBox "0 0 20 20"
    :fill "currentColor"}
   [:path
    {:fill-rule "evenodd"
     :d (if open?
          "M14.77 12.79a.75.75 0 01-1.06-.02L10 8.832 6.29 12.77a.75.75 0 11-1.08-1.04l4.25-4.5a.75.75 0 011.08 0l4.25 4.5a.75.75 0 01-.02 1.06z"
          "M5.23 7.21a.75.75 0 011.06.02L10 11.168l3.71-3.938a.75.75 0 111.08 1.04l-4.25 4.5a.75.75 0 01-1.08 0l-4.25-4.5a.75.75 0 01.02-1.06z")}]])

(defn paginated-dropdown
  "Um dropdown paginado com funcionalidade de busca

   Parâmetros:
   - options: lista de opções [{:value \"id\" :label \"texto\" :description \"descrição\"}]
   - loading?: booleano indicando se está carregando dados
   - on-search: função chamada quando o usuário digita na busca
   - on-page-change: função chamada quando o usuário muda de página
   - on-select: função chamada quando o usuário seleciona uma opção
   - selected-value: valor atualmente selecionado
   - placeholder: texto a mostrar quando nada está selecionado
   - total-items: número total de itens
   - current-page: página atual
   - items-per-page: itens por página"
  [{:keys [options loading? on-search on-page-change
           on-select selected-value placeholder
           total-items current-page items-per-page]}]
  (let [open? (r/atom false)
        search-term (r/atom "")
        debounce-timer (atom nil)]
    (fn [{:keys [options loading? on-search on-page-change
                 on-select selected-value placeholder
                 total-items current-page items-per-page]}]
      [:div.relative
       ;; Header do dropdown (o que é mostrado quando fechado)
       [:div.dropdown-header.cursor-pointer.flex.items-center.justify-between.px-4.py-2.border.rounded.shadow-sm
        {:on-click #(swap! open? not)}
        [:div
         (if selected-value
           (if-let [selected-option (first (filter #(= (:value %) selected-value) options))]
             (:label selected-option)
             (or placeholder "Select an option"))
           (or placeholder "Select an option"))]
        [chevron-icon @open?]]

       ;; Conteúdo do dropdown (aparece quando aberto)
       (when @open?
         [:div.absolute.left-0.right-0.mt-1.border.rounded.bg-white.shadow-lg.z-50
          ;; Campo de busca
          [:div.p-2.border-b
           [:div.relative
            [:span.absolute.inset-y-0.left-0.flex.items-center.pl-2
             [search-icon]]
            [:input.w-full.pl-8.pr-2.py-2.border.rounded
             {:type "text"
              :placeholder "Search options..."
              :value @search-term
              :on-change (fn [e]
                           (let [value (.. e -target -value)]
                             (reset! search-term value)
                             (when @debounce-timer
                               (js/clearTimeout @debounce-timer))
                             (reset! debounce-timer
                                     (js/setTimeout #(on-search value) 300))))}]]]

          ;; Lista de opções
          (cond
            loading?
            [:div.p-4.text-center.text-sm.text-gray-500 "Loading..."]

            (empty? options)
            [:div.p-4.text-center.text-sm.text-gray-500 "No options found"]

            :else
            [:div.max-h-60.overflow-y-auto
             (for [option options]
               ^{:key (:value option)}
               [:div.px-4.py-2.hover:bg-gray-100.cursor-pointer
                {:on-click #(do (on-select (:value option))
                                (reset! open? false))}
                [:div.font-medium (:label option)]
                (when (:description option)
                  [:div.text-sm.text-gray-500 (:description option)])])])

          ;; Paginação
          [:div.flex.items-center.justify-between.px-4.py-2.border-t.text-sm
           [:div.text-xs.text-gray-500
            (str "Showing "
                 (inc (* (dec current-page) items-per-page)) "-"
                 (min (* current-page items-per-page) total-items)
                 " of " total-items)]
           [:div.flex.space-x-2
            [:button.px-2.py-1.border.rounded
             {:type "button"
              :disabled (= current-page 1)
              :class (if (= current-page 1) "opacity-50 cursor-not-allowed" "hover:bg-gray-100")
              :on-click #(when (> current-page 1)
                           (on-page-change (dec current-page)))}
             "←"]
            [:div (str current-page "/" (js/Math.ceil (/ total-items items-per-page)))]
            [:button.px-2.py-1.border.rounded
             {:type "button"
              :disabled (>= (* current-page items-per-page) total-items)
              :class (if (>= (* current-page items-per-page) total-items) "opacity-50 cursor-not-allowed" "hover:bg-gray-100")
              :on-click #(when (< (* current-page items-per-page) total-items)
                           (on-page-change (inc current-page)))}
             "→"]]]])])))
