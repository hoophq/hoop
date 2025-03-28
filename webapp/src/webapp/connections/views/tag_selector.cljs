(ns webapp.connections.views.tag-selector
  (:require ["lucide-react" :refer [Tag Check ChevronRight ChevronLeft]]
            ["@radix-ui/themes" :refer [Box Button Flex Text Separator]]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.searchbox :as searchbox]))

;; Event para buscar as tags disponíveis
(rf/reg-event-fx
 :connections->get-connection-tags
 (fn [{:keys [db]} [_]]
   {:db (assoc-in db [:connections :tags-loading] true)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri "/connection-tags"
                             :on-success (fn [response]
                                           (rf/dispatch [:connections->set-connection-tags (:items response)]))}]]]}))

;; Event para armazenar as tags no app-db
(rf/reg-event-db
 :connections->set-connection-tags
 (fn [db [_ tags]]
   (-> db
       (assoc-in [:connections :tags] tags)
       (assoc-in [:connections :tags-loading] false))))

;; Subscription para obter as tags
(rf/reg-sub
 :connections->tags
 (fn [db]
   (get-in db [:connections :tags])))

;; Subscription para o status de carregamento das tags
(rf/reg-sub
 :connections->tags-loading?
 (fn [db]
   (get-in db [:connections :tags-loading])))

;; Função para extrair o nome da chave (última parte após a última barra)
(defn get-key-name [key]
  (let [parts (cs/split key #"/")]
    (last parts)))

;; Função auxiliar para agrupar tags por chave
(defn group-tags-by-key [tags]
  (reduce
   (fn [result tag]
     (let [key (:key tag)
           value (:value tag)]
       (update result key (fnil conj []) value)))
   {}
   tags))

;; Função para converter as tags selecionadas em string para query params
(defn tags-to-query-string [selected-tags]
  (cs/join ","
           (for [[key values] selected-tags
                 value values]
             (str key "=" value))))

(defn get-display-key [key]
  (let [display-keys (cs/split (get-key-name key) ".")
        display-key (if (= (count display-keys) 1)
                      (first display-keys)
                      (second display-keys))]
    display-key))

;; Componente de visualização da tela de valores
(defn values-view [key values display-key selected-values on-change on-back]
  [:div {:class "w-full"}
   ;; Cabeçalho com back e clear
   [:> Flex {:justify "between" :align "center" :class "mb-3"}
    [:> Button {:variant "ghost" :color "gray" :onClick on-back}
     [:> ChevronLeft {:size 16}]
     [:> Text {:size "1" :class "text-gray-11"}
      "Back"]]
    [:> Button {:variant "ghost" :color "gray" :onClick #(on-change (dissoc selected-values key))}
     [:> Text {:size "1" :class "text-gray-11"}
      "Clear"]]]

   [:> Separator {:class "w-full"}]

   [:> Text {:as "p" :size "1" :mt "3" :class "text-gray-11"}
    (get-display-key display-key)]
   ;; Lista de valores
   [:div {:class "space-y-1 px-2 pt-3"}
    [:> Button {:variant "ghost"
                :color "gray"
                :class "w-full justify-between"
                :onClick #(on-change (assoc selected-values key values))}
     [:> Text {:size "2" :class "text-gray-12"}
      "Select all"]]

    (for [value values
          :let [is-selected (some #(= % value) (get selected-values key []))]]
      ^{:key (str key "-" value)}
      [:> Button {:variant "ghost"
                  :color "gray"
                  :class "w-full justify-between"
                  :onClick (fn [_]
                             (let [new-values (if is-selected
                                                (remove (fn [v] (= v value)) (get selected-values key []))
                                                (conj (or (get selected-values key []) []) value))
                                   new-selected (if (empty? new-values)
                                                  (dissoc selected-values key)
                                                  (assoc selected-values key new-values))]
                               (on-change new-selected)))}
       [:> Text {:size "2" :class "text-gray-12"}
        value]
       (when is-selected
         [:> Check {:size 16}])])]])

;; Componente para a exibição da lista de chaves
(defn keys-view [grouped-tags search-term selected-values on-change on-select-key]
  (let [;; Função para filtrar chaves e valores com base no termo de busca
        filtered-keys (if (empty? search-term)
                        ;; Sem filtro - mostrar todas as chaves
                        (keys grouped-tags)
                        ;; Com filtro - filtrar por chave ou valor
                        (filter (fn [key]
                                  (let [display-key (get-key-name key)
                                        values (get grouped-tags key)]
                                    (or
                                     ;; Match na chave
                                     (cs/includes? (cs/lower-case display-key)
                                                   (cs/lower-case search-term))
                                     ;; Match em algum valor
                                     (some #(cs/includes?
                                             (cs/lower-case %)
                                             (cs/lower-case search-term))
                                           values))))
                                (keys grouped-tags)))]
     ;; Lista de chaves
    [:div {:class "w-full max-h-64 overflow-y-auto"}
     [:> Box {:class "space-y-1 p-2"}
      (for [key filtered-keys
            :let [display-key (get-key-name key)
                  selected-count (count (get selected-values key []))]]
        ^{:key key}
        [:> Button {:variant "ghost"
                    :color "gray"
                    :onClick #(on-select-key key)
                    :class "w-full justify-between gap-3"}
         [:> Text {:size "2" :class "truncate text-gray-12"}
          (get-display-key display-key)]
         [:> Flex {:align "center" :gap "2"}
          (when (pos? selected-count)
            [:> Box {:class "flex items-center justify-center rounded-full h-5 w-5 bg-gray-11"}
             [:> Text {:size "1" :weight "bold" :class "text-white"}
              selected-count]])
          [:> ChevronRight {:size 16}]]])]]))

;; Componente para exibir resultados de busca
(defn search-results-view [grouped-tags search-term selected-values on-change on-select-key]
  (let [search-results (for [[key values] grouped-tags
                             value values
                             :let [display-key (get-key-name key)
                                   key-match? (cs/includes? (cs/lower-case display-key)
                                                            (cs/lower-case search-term))
                                   value-match? (cs/includes? (cs/lower-case value)
                                                              (cs/lower-case search-term))]
                             :when (or key-match? value-match?)]
                         {:key key
                          :value value
                          :display-key display-key})]
     ;; Resultados da busca
    [:> Box {:class "w-full max-h-64 overflow-y-auto"}
     [:> Box {:class "space-y-1 p-2"}
      (for [{:keys [key value display-key]} search-results]
        ^{:key (str key "-" value)}
        [:> Button {:variant "ghost"
                    :color "gray"
                    :class "w-full justify-between gap-2"
                    :onClick #(on-select-key key)}
         [:> Flex {:direction "column" :align "start"}
          [:> Text {:size "2" :class "text-gray-12"} value]
          [:> Text {:size "1" :class "text-gray-11"} (str "Value | " display-key)]]
         [:> ChevronRight {:size 16}]])]]))

;; Componente principal do seletor de tags
(defn tag-selector [selected-tags on-change]
  (let [all-tags (rf/subscribe [:connections->tags])
        loading? (rf/subscribe [:connections->tags-loading?])
        search-term (r/atom "")
        selected-values (r/atom (or selected-tags {}))
        current-view (r/atom :keys) ;; :keys, :values ou :search
        current-key (r/atom nil)]

    (fn [selected-tags on-change]
      (let [grouped-tags (group-tags-by-key @all-tags)
            has-search-results? (and (not-empty @search-term)
                                     (not= @current-view :values))]

        [:> Box
         ;; Input de busca
         (when (or (= @current-view :keys)
                   (= @current-view :search))
           [:div {:class "mb-3"}
            [searchbox/main
             {:value @search-term
              :on-change (fn [new-term]
                           (reset! search-term new-term)
                           (when (not-empty new-term)
                             (reset! current-view :search)
                             (reset! current-key nil)))
              :placeholder "Search Tags"
              :display-key :text
              :searchable-keys [:text]
              :hide-results-list true
              :size :small
              :variant :small}]])

         ;; Conteúdo principal - muda dependendo da visualização atual
         (cond
           ;; Visualização de um valor específico
           (= @current-view :values)
           [values-view
            @current-key
            (get grouped-tags @current-key)
            (get-key-name @current-key)
            @selected-values
            (fn [new-values]
              (reset! selected-values new-values)
              (on-change new-values))
            (fn []
              (reset! current-view :keys)
              (reset! search-term ""))]

           ;; Resultados de busca
           has-search-results?
           [search-results-view grouped-tags @search-term @selected-values
            (fn [new-values]
              (reset! selected-values new-values)
              (on-change new-values))
            (fn [key]
              (reset! current-key key)
              (reset! current-view :values))]

           ;; Visualização padrão - lista de chaves
           :else
           (do
             (when (not= @current-view :keys)
               (reset! current-view :keys))

              ;; Lista de chaves
             (if @loading?
               [:div {:class "text-center"} "Loading tags..."]
               [keys-view grouped-tags @search-term @selected-values
                (fn [new-values]
                  (reset! selected-values new-values)
                  (on-change new-values))
                (fn [key]
                  (reset! current-key key)
                  (reset! current-view :values))])))]))))
