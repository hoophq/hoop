;; (ns webapp.guardrails.create-update
;;   (:require
;;    ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Table Text]]
;;    ["lucide-react" :refer [ArrowLeft]]
;;    [re-frame.core :as rf]
;;    [reagent.core :as r]
;;    [webapp.components.forms :as forms]
;;    [webapp.components.loaders :as loaders]
;;    [webapp.components.multiselect :as multi-select]))

;; (def select-state (r/atom false))

;; (defn all-rows-selected? [rules]
;;   (every? :selected rules))

;; (defn create-empty-rule []
;;   {:type ""
;;    :rule ""
;;    :pattern ""
;;    :words []
;;    :selected false})

;; (defn rule-details [rule state idx update-rule-field words-state update-word-input pattern-state update-pattern-input]
;;   (let [input-value (get @words-state idx "")
;;         pattern-value (get @pattern-state idx "")]
;;     (cond
;;       (and (= "custom-rule" (:rule rule))
;;            (= "patter-match" (:type rule)))
;;       [forms/input
;;        {:placeholder "Describe how this is used in your connections"
;;         :on-change #(update-pattern-input pattern-state idx (-> % .-target .-value))
;;         :value pattern-value}]

;;       (and (= "custom-rule" (:rule rule))
;;            (= "deny-word" (:type rule)))
;;       [multi-select/text-input
;;        {:value (if (empty? (:words rule))
;;                  []
;;                  (mapv #(into {} {"value" % "label" %}) (:words rule)))
;;         :input-value input-value
;;         :on-change (fn [value]
;;                      (update-rule-field
;;                       state
;;                       idx
;;                       :words
;;                       (mapv #(get % "value") value)))
;;         :on-input-change #(update-word-input words-state idx %)
;;         :id (str "deny-words-" idx)
;;         :name (str "deny-words-" idx)}])))

;; (defn rules-table [{:keys [title
;;                            state
;;                            toggle-rule-selection
;;                            update-rule-field
;;                            words-state
;;                            update-word-input
;;                            pattern-state
;;                            update-pattern-input]}]
;;   [:> Box {:class "space-y-radix-5"}
;;    [:> Text {:size "3" :weight "bold" :class "text-[--gray-12]"}
;;     title]
;;    [:> Table.Root {:size "2" :variant "surface"}
;;     [:> Table.Header
;;      [:> Table.Row
;;       (when @select-state
;;         [:> Table.ColumnHeaderCell ""])
;;       [:> Table.ColumnHeaderCell "Type"]
;;       [:> Table.ColumnHeaderCell "Rule"]
;;       [:> Table.ColumnHeaderCell "Details"]]]
;;     [:> Table.Body
;;      (doall
;;       (for [[idx rule] (map-indexed vector @state)]
;;         ^{:key (str (hash (str rule idx)))}
;;         [:> Table.Row
;;          (when @select-state
;;            [:> Table.RowHeaderCell {:width "20px"}
;;             [:input {:type "checkbox"
;;                      :checked (:selected rule)
;;                      :on-change #(toggle-rule-selection state idx)}]])

;;          [:> Table.RowHeaderCell {:width "160px"}
;;           [forms/select {:size "2"
;;                          :name "type"
;;                          :variant "ghost"
;;                          :not-margin-bottom? true
;;                          :on-change #(update-rule-field state idx :type %)
;;                          :selected (:type rule)
;;                          :full-width? true
;;                          :options [{:value "deny-word" :text "Deny Word"}
;;                                    {:value "patter-match" :text "Pattern Match"}]}]]
;;          [:> Table.Cell {:width "220px"}
;;           [forms/select {:size "2"
;;                          :name "rule"
;;                          :variant "ghost"
;;                          :not-margin-bottom? true
;;                          :on-change #(update-rule-field state idx :rule %)
;;                          :selected (:rule rule)
;;                          :full-width? true
;;                          :options [{:value "require-where" :text "Require WHERE clause"}
;;                                    {:value "block-password" :text "Block Passwords"}
;;                                    {:value "custom-rule" :text "Custom rule"}]}]]
;;          [:> Table.Cell
;;           [rule-details rule state idx update-rule-field words-state update-word-input pattern-state update-pattern-input]]]))]]])

;; (defn guardrail-form [form-type guardrails scroll-pos]
;;   (let [id (r/atom (or (:id guardrails) ""))
;;         name (r/atom (or (:name guardrails) ""))
;;         description (r/atom (or (:description guardrails) ""))
;;         input (r/atom (or (:input guardrails) [(create-empty-rule)]))
;;         output (r/atom (or (:output guardrails) [(create-empty-rule)]))

;;         input-pattern (r/atom {})
;;         output-pattern (r/atom {})
;;         input-words (r/atom {})  ;; map de idx -> input-value
;;         output-words (r/atom {}) ;; map de idx -> input-value

;;         update-word-input (fn [words-atom idx value]
;;                             (swap! words-atom assoc idx value))
;;         update-pattern-input (fn [pattern-atom idx value]
;;                                (swap! pattern-atom assoc idx value))
;;         ;; Funções de manipulação das regras
;;         update-rule-field (fn [rules-atom idx field value]
;;                             (swap! rules-atom assoc-in [idx field] value))

;;         toggle-rule-selection (fn [rules-atom idx]
;;                                 (swap! rules-atom update-in [idx :selected] not))

;;         toggle-all-selections (fn [rules-atom]
;;                                 (let [all-selected? (every? :selected @rules-atom)]
;;                                   (swap! rules-atom
;;                                          (fn [rules]
;;                                            (mapv #(assoc % :selected (not all-selected?)) rules)))))

;;         remove-selected-rules (fn [rules-atom]
;;                                 (let [filtered-rules (vec (remove :selected @rules-atom))]
;;                                   (reset! rules-atom
;;                                           (if (empty? filtered-rules)
;;                                             [(create-empty-rule)]
;;                                             filtered-rules))))

;;         add-new-rule (fn [rules-atom]
;;                        (println @rules-atom)
;;                        (swap! rules-atom conj (create-empty-rule)))]
;;     (fn []
;;       [:> Box
;;        [:> Flex {:p "5" :gap "2"}
;;         [:> Button {:variant "ghost"
;;                     :size "2"
;;                     :color "gray"
;;                     :on-click #(js/history.back)}
;;          [:> ArrowLeft {:size 16}]
;;          "Back"]]
;;        [:> Box {:p "7"}
;;         [:> Flex {:justify "between"
;;                   :align "center"
;;                   :pb "7"
;;                   :class (str "sticky top-0 z-10 -m-10 mb-0 p-10 bg-gray-1 "
;;                               (if (>= @scroll-pos 30)
;;                                 " border-b border-[--gray-a6]"
;;                                 " "))}
;;          [:> Heading {:as "h2" :size "8"}
;;           "Create a new Guardrail"]
;;          [:> Flex {:gap "5" :align "center"}
;;           (when (= :editor-plugin form-type)
;;             [:> Button {:size "4"
;;                         :variant "ghost"
;;                         :color "red"
;;                         :on-click #(rf/dispatch [:guardrails->delete-by-id id])}
;;              "Delete"])
;;           [:> Button {:size "4"
;;                       :on-click (fn []
;;                                   (let [data {:id (:id guardrails)
;;                                               :name name
;;                                               :description description
;;                                               :input input
;;                                               :output output}]
;;                                     (if (= :edit form-type)
;;                                       (rf/dispatch [:guardrails->update-by-id data])
;;                                       (rf/dispatch [:guardrails->create data]))))}
;;            "Save"]]]

;;         [:> Box {:py "7" :pr "7" :class "space-y-radix-9"}
;;          [:> Grid {:columns "7" :gap "7"}
;;           [:> Box {:grid-column "span 2 / span 2"}
;;            [:h3 {:class "text-sm font-semibold mb-2"} "Set Guardrail information"]
;;            [:p {:class "text-sm text-gray-500"} "Used to identify your Guardrail in your connections."]]

;;           [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
;;            [forms/input {:label "Name"
;;                          :placeholder "Sensitive Data"
;;                          :required true
;;                          :on-change #(reset! name (-> % .-target .-value))
;;                          :value @name}]
;;            [forms/input {:label "Description (Optional)"
;;                          :placeholder "Describe how this is used in your connections"
;;                          :required true
;;                          :on-change #(reset! description (-> % .-target .-value))
;;                          :value (:description guardrails)}]]]

;;          [:>  Grid {:columns "7" :gap "7"}
;;           [:> Box {:grid-column "span 2 / span 2"}
;;            [:h3 {:class "text-lg font-semibold mt-8"} "Configure rules"]
;;            [:p {:class "text-sm text-gray-500 mb-4"} "Setup rules with Presets or Custom regular expression scripts."]]

;;           [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
;;            [:> Box  {:class "space-y-radix-5"}
;;             [rules-table
;;              {:title "Input rules"
;;               :state input
;;               :words-state input-words
;;               :pattern-state input-pattern
;;               :update-word-input update-word-input
;;               :update-pattern-input update-pattern-input
;;               :toggle-rule-selection toggle-rule-selection
;;               :update-rule-field update-rule-field}]
;;             [:> Flex {:gap "2"}
;;              [:> Button {:variant "soft"
;;                          :size "2"
;;                          :on-click #(add-new-rule input)}
;;               "+ New"]
;;              [:> Button {:variant "soft"
;;                          :size "2"
;;                          :color "gray"
;;                          :on-click #(reset! select-state (not @select-state))}
;;               (if @select-state
;;                 "Cancel"
;;                 "Select")]
;;              (when @select-state
;;                [:<>
;;                 [:> Button {:variant "outline"
;;                             :size "2"
;;                             :color "gray"
;;                             :on-click #(toggle-all-selections input)}
;;                  (if (all-rows-selected? (:input guardrails))
;;                    "Deselect all"
;;                    "Select all")]
;;                 [:> Button {:variant "outline"
;;                             :size "2"
;;                             :color "red"
;;                             :on-click #(remove-selected-rules input)}
;;                  "Delete"]])]]

;;            [:> Box {:class "space-y-radix-5"}
;;             [rules-table
;;              {:title "Output rules"
;;               :state output
;;               :words-state output-words
;;               :pattern-state output-pattern
;;               :update-word-input update-word-input
;;               :update-pattern-input update-pattern-input
;;               :toggle-rule-selection toggle-rule-selection
;;               :update-rule-field update-rule-field}]
;;             [:> Flex {:gap "2"}
;;              [:> Button {:variant "soft"
;;                          :size "2"
;;                          :on-click #(add-new-rule output)}
;;               "+ New"]
;;              [:> Button {:variant "soft"
;;                          :size "2"
;;                          :color "gray"
;;                          :on-click #(reset! select-state (not @select-state))}
;;               (if @select-state
;;                 "Cancel"
;;                 "Select")]

;;              (when @select-state
;;                [:<>
;;                 [:> Button {:variant "outline"
;;                             :size "2"
;;                             :color "gray"
;;                             :on-click #(toggle-all-selections output)}
;;                  (if (all-rows-selected? (:output guardrails))
;;                    "Deselect all"
;;                    "Select all")]
;;                 [:> Button {:variant "outline"
;;                             :size "2"
;;                             :color "red"
;;                             :on-click  #(remove-selected-rules output)}
;;                  "Delete"]])]]]]]]])))

;; (defn- loading []
;;   [:div {:class "flex items-center justify-center rounded-lg border bg-white h-full"}
;;    [:div {:class "flex items-center justify-center h-full"}
;;     [loaders/simple-loader]]])

;; (defn main [_]
;;   (let [guardrails->active-guardrail (rf/subscribe [:guardrails->active-guardrail])
;;         scroll-pos (r/atom 0)]
;;     (fn [form-type]
;;       (r/with-let [handle-scroll (fn []
;;                                    (reset! scroll-pos (.-scrollY js/window)))]
;;         (.addEventListener js/window "scroll" handle-scroll)
;;         (finally
;;           (.removeEventListener js/window "scroll" handle-scroll)))
;;       (if (= :loading (:status @guardrails->active-guardrail))
;;         [loading]
;;         [guardrail-form form-type (:data @guardrails->active-guardrail) scroll-pos]))))
