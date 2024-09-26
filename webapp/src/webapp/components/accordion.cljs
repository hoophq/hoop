(ns webapp.components.accordion
  (:require
   ["@radix-ui/react-accordion" :as Accordion]
   ["@radix-ui/react-icons" :refer [ChevronDownIcon]]
   ["@radix-ui/themes" :refer [Box Flex Text Avatar Checkbox Button Badge]]
   ["lucide-react" :refer [Check ChevronRight]]
   [reagent.core :as r]))

;; Componente de Badge
(defn badge [text]
  [:> Badge {:color "red" :variant "soft" :size "2"} text])

;; Botão de configurar
(defn configure-button []
  [:> Button {:variant "soft" :size "3"} "Configure"])

;; Checkbox usando o tema do Radix
(defn checkbox []
  [:> Checkbox])

;; Indicadores de status (ícones de checkmark e loading)
(defn status-icon []
  [:> Avatar {:size "1"
              :variant "soft"
              :color "green"
              :radius "full"
              :fallback (r/as-element
                         [:> Check {:size 16
                                    :color "green"}])}])

;; Accordion Item parametrizável
(defn accordion-item [{:keys [title subtitle content value status show-checkbox? show-badge? show-configure? show-icon? total-items]}]
  [:> (.-Item Accordion)
   {:value value
    :className (str "first:rounded-t-lg last:rounded-b-lg bg-[--accent-2] border-[--gray-a6] "
                    (when (> total-items 1) "border first:border-b-0 last:border-t-0")
                    (when (= total-items 1) "border"))}
   ;; AccordionTrigger para abrir e fechar
   [:> (.-Header Accordion) {:className "group flex justify-between items-center w-full p-5"}
     ;; Checkbox (opcional)
    [:> Flex {:align "center" :gap "5"}
     (when show-checkbox?
       [checkbox])

     [:> Avatar {:size "5"
                 :variant "soft"
                 :radius "medium"
                 :color "gray"
                 :fallback (r/as-element
                            [:> Check {:size 20
                                       :color "gray"}])}]

     ;; Título e subtítulo
     [:div {:className "flex flex-col"}
      [:> Text {:size "5" :weight "bold" :className "text-[--gray-12]"} title] ;; Título
      [:> Text {:size "3" :className "text-[--gray-11]"} subtitle]]] ;; Subtítulo

     ;; Elementos à direita
    [:div {:className "flex space-x-3 items-center"}
      ;; Badge (opcional)
     (when show-badge? [badge "Badge"])
      ;; Botão de configurar (opcional)
     (when show-configure? [configure-button])
      ;; Ícone de status (opcional)
     (when show-icon? [status-icon status])
      ;; Chevron para abrir/fechar
     [:> (.-Trigger Accordion)
      [:> ChevronRight {:size 16 :className "text-[--gray-12] transition-transform duration-300 group-data-[state=open]:rotate-90"}]]]]

   ;; Conteúdo que aparece ao expandir
   [:> (.-Content Accordion)
    [:> Box {:px "5" :py "7" :className "bg-white border-t border-[--gray-a6] rounded-b-lg"}
     content]]])

;; Accordion Root parametrizável
(defn accordion-root [items]
  [:> (.-Root Accordion)
   {:className "w-full"
    :type "single"
    :defaultValue (:value (first items))
    :collapsible true}
   (for [{:keys [value] :as item} items]
     ^{:key value} [accordion-item (merge item {:total-items (count items)})])])
