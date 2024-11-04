(ns webapp.guardrails.create-update
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Table Text]]
   ["lucide-react" :refer [ArrowLeft]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

(def guardrails-name (r/atom ""))
(def description (r/atom ""))
(def select-state (r/atom false))

(defn all-rows-selected? [rules]
  (every? :selected rules))

(defn input-field [input label placeholder]
  [forms/input {:label label
                :placeholder placeholder
                :required true
                :on-change #(reset! input (-> % .-target .-value))
                :value @input}])

(defn rules-table [{:keys [title state type]}]
  [:> Box {:class "space-y-radix-5"}
   [:> Text {:size "3" :weight "bold" :class "text-[--gray-12]"}
    title]
   [:> Table.Root {:size "2" :variant "surface"}
    [:> Table.Header
     [:> Table.Row
      (when @select-state
        [:> Table.ColumnHeaderCell ""])
      [:> Table.ColumnHeaderCell "Type"]
      [:> Table.ColumnHeaderCell "Rule"]
      [:> Table.ColumnHeaderCell "Details"]]]
    [:> Table.Body
     (doall
      (for [[idx rule] (map-indexed vector (get @state (keyword type)))]
        ^{:key (str (hash (str rule idx)))}
        [:> Table.Row
         (when @select-state
           [:> Table.RowHeaderCell {:width "20px"}
            [:input {:type "checkbox"
                     :checked (:selected rule)
                     :on-change #(rf/dispatch [:guardrails->toggle-select-row type idx])}]])

         [:> Table.RowHeaderCell {:width "160px"}
          [forms/select {:size "2"
                         :name "type"
                         :variant "ghost"
                         :not-margin-bottom? true
                         :on-change #(rf/dispatch [:guardrails->push-rule type :type % idx])
                         :selected (:type rule)
                         :full-width? true
                         :options [{:value "deny-word" :text "Deny Word"}
                                   {:value "patter-match" :text "Pattern Match"}]}]]
         [:> Table.Cell {:width "220px"}
          [forms/select {:size "2"
                         :name "rule"
                         :variant "ghost"
                         :not-margin-bottom? true
                         :on-change #(rf/dispatch [:guardrails->push-rule type :rule % idx])
                         :selected (:rule rule)
                         :full-width? true
                         :options [{:value "where-clause" :text "Require WHERE clause"}
                                   {:value "block-password" :text "Block Passwords"}
                                   {:value "custom-rule" :text "Custom rule"}]}]]
         [:> Table.Cell (:details rule)]]))]]])

(defn guardrail-form [guardrails scroll-pos]
  [:> Box
   [:> Flex {:p "5" :gap "2"}
    [:> Button {:variant "ghost"
                :size "2"
                :color "gray"
                :on-click #(js/history.back)}
     [:> ArrowLeft {:size 16}]
     "Back"]]
   [:> Box {:p "7"}
    [:> Flex {:justify "between"
              :align "center"
              :pb "7"
              :class (str "sticky top-0 z-10 -m-10 mb-0 p-10 bg-gray-1 "
                          (if (>= @scroll-pos 30)
                            " border-b border-[--gray-a6]"
                            " "))}
     [:> Heading {:as "h2" :size "8"}
      "Create a new Guardrail"]
     [:> Button {:size "4"}
      "Save"]]

    [:> Box {:py "7" :pr "7" :class "space-y-radix-9"}
     [:> Grid {:columns "7" :gap "7"}
      [:> Box {:grid-column "span 2 / span 2"}
       [:h3 {:class "text-sm font-semibold mb-2"} "Set Guardrail information"]
       [:p {:class "text-sm text-gray-500"} "Used to identify your Guardrail in your connections."]]

      [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
       [input-field guardrails-name "Name" "Sensitive Data"]
       [input-field description "Description (Optional)" "Describe how this is used in your connections"]]]

     [:>  Grid {:columns "7" :gap "7"}
      [:> Box {:grid-column "span 2 / span 2"}
       [:h3 {:class "text-lg font-semibold mt-8"} "Configure rules"]
       [:p {:class "text-sm text-gray-500 mb-4"} "Setup rules with Presets or Custom regular expression scripts."]]

      [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
       [:> Box  {:class "space-y-radix-5"}
        [rules-table
         {:title "Input rules"
          :state guardrails
          :type :input}]
        [:> Flex {:gap "2"}
         [:> Button {:variant "soft"
                     :size "2"
                     :on-click #(rf/dispatch [:guardrails->add-new-rule-line :input])}
          "+ New"]
         [:> Button {:variant "soft"
                     :size "2"
                     :color "gray"
                     :on-click #(reset! select-state (not @select-state))}
          (if @select-state
            "Cancel"
            "Select")]
         (when @select-state
           [:<>
            [:> Button {:variant "outline"
                        :size "2"
                        :color "gray"
                        :on-click #(if (all-rows-selected? (:input @guardrails))
                                     (rf/dispatch [:guardrails->deselect-all-rows :input])
                                     (rf/dispatch [:guardrails->select-all-rows :input]))}
             (if (all-rows-selected? (:input @guardrails))
               "Deselect all"
               "Select all")]
            [:> Button {:variant "outline"
                        :size "2"
                        :color "red"
                        :on-click #(rf/dispatch [:guardrails->remove-rules :input])} "Delete"]])]]

       [:> Box {:class "space-y-radix-5"}
        [rules-table {:title "Input rules"
                      :state guardrails
                      :type :output}]
        [:> Flex {:gap "2"}
         [:> Button {:variant "soft"
                     :size "2"
                     :on-click #(rf/dispatch [:guardrails->add-new-rule-line :output])}
          "+ New"]
         [:> Button {:variant "soft"
                     :size "2"
                     :color "gray"
                     :on-click #(reset! select-state (not @select-state))}
          (if @select-state
            "Cancel"
            "Select")]

         (when @select-state
           [:<>
            [:> Button {:variant "outline"
                        :size "2"
                        :color "gray"
                        :on-click #(if (all-rows-selected? (:output @guardrails))
                                     (rf/dispatch [:guardrails->deselect-all-rows :output])
                                     (rf/dispatch [:guardrails->select-all-rows :output]))}
             (if (all-rows-selected? (:output @guardrails))
               "Deselect all"
               "Select all")]
            [:> Button {:variant "outline"
                        :size "2"
                        :color "red"
                        :on-click  #(rf/dispatch [:guardrails->remove-rules :output])} "Delete"]])]]]]]]])

(defn main []
  (let [guardrails->active-guardrail (rf/subscribe [:guardrails->active-guardrail])
        scroll-pos (r/atom 0)]
    (fn []
      (r/with-let [handle-scroll (fn []
                                   (reset! scroll-pos (.-scrollY js/window)))]
        (.addEventListener js/window "scroll" handle-scroll)
        (finally
          (.removeEventListener js/window "scroll" handle-scroll)))
      [guardrail-form guardrails->active-guardrail scroll-pos])))
