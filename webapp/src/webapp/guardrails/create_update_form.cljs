(ns webapp.guardrails.create-update-form
  (:require
   [re-frame.core :as rf]
   [reagent.core :as r]
   ["@radix-ui/themes" :refer [Badge Box Heading Flex Grid Text]]
   [webapp.components.loaders :as loaders]
   [webapp.components.multiselect :as multiselect]
   [webapp.guardrails.helpers :as helpers]
   [webapp.guardrails.form-header :as form-header]
   [webapp.guardrails.basic-info :as basic-info]
   [webapp.guardrails.connections-section :as connections-section]
   [webapp.guardrails.rules-table :as rules-table]))

(defn guardrail-form [form-type guardrails scroll-pos free-license?]
  (let [state (helpers/create-form-state guardrails)
        handlers (helpers/create-form-handlers state)
        attributes-data (rf/subscribe [:attributes/list-data])]
    (fn []
      (let [all-attributes (or @attributes-data [])]
        [:> Box {:class "min-h-screen bg-gray-1"}
         [:form {:id "guardrails-form"
                 :on-submit (fn [e]
                              (.preventDefault e)
                              (let [data {:id @(:id state)
                                          :name @(:name state)
                                          :description @(:description state)
                                          :connection_ids @(:connection-ids state)
                                          :attributes @(:attribute-names state)
                                          :input @(:input state)
                                          :output @(:output state)}]
                                (if (= :edit form-type)
                                  (rf/dispatch [:guardrails->update-by-id data])
                                  (rf/dispatch [:guardrails->create data]))))}
          [form-header/main
           {:form-type form-type
            :id @(:id state)
            :scroll-pos scroll-pos}]

          [:> Box {:p "7" :class "space-y-radix-9"}
           [basic-info/main
            {:name (:name state)
             :description (:description state)
             :on-name-change #(reset! (:name state) %)
             :on-description-change #(reset! (:description state) %)}]

           ;; Connections section
           [connections-section/main
            {:connection-ids (:connection-ids state)
             :on-connections-change (:on-connections-change handlers)}]

           ;; Attributes section
           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Heading {:as "h3" :size "4" :weight "medium"} "Attribute configuration"]
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "Select which Attributes to apply this configuration."]]
            [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
             [multiselect/main
              {:label "Attributes"
               :id "attribute-names-input"
               :name "attribute-names-input"
               :options (mapv #(hash-map :value (:name %) :label (:name %)) all-attributes)
               :default-value (mapv #(hash-map :value % :label %) @(:attribute-names state))
               :placeholder "Select attributes..."
               :on-change (fn [selected-options]
                            (let [names (mapv :value (js->clj selected-options :keywordize-keys true))]
                              (reset! (:attribute-names state) names)))}]]]

           ;; Rules section
           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Flex {:align "center" :gap "2"}
              [:> Heading {:as "h3" :size "4" :weight "medium"} "Configure rules"]
              [:> Badge {:variant "solid" :color "green" :size "1"}
               "Beta"]]
             [:p.text-sm.text-gray-500.mb-4
              "Setup rules with Presets or Custom regular expression scripts."]]

            [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
             ;; Input Rules
             [rules-table/main
              (merge
               {:title "Input rules"
                :free-license? free-license?
                :state (:input state)
                :words-state (:input-words state)
                :pattern-state (:input-pattern state)
                :select-state (:input-select state)}
               handlers)]

             ;; Output Rules
             [rules-table/main
              (merge
               {:title "Output rules"
                :free-license? free-license?
                :state (:output state)
                :words-state (:output-words state)
                :pattern-state (:output-pattern state)
                :select-state (:output-select state)}
               handlers)]]]]]]))))

(defn- loading []
  [:div {:class "flex items-center justify-center rounded-lg border bg-white h-full"}
   [:div {:class "flex items-center justify-center h-full"}
    [loaders/simple-loader]]])

(defn main [form-type]
  (let [guardrails->active-guardrail (rf/subscribe [:guardrails->active-guardrail])
        user (rf/subscribe [:users->current-user])
        scroll-pos (r/atom 0)]

    (rf/dispatch [:attributes/list])

    (fn []
      (r/with-let [handle-scroll #(reset! scroll-pos (.-scrollY js/window))]
        (.addEventListener js/window "scroll" handle-scroll)
        (if (= :loading (:status @guardrails->active-guardrail))
          [loading]
          [guardrail-form form-type
           (:data @guardrails->active-guardrail)
           scroll-pos
           (-> @user :data :free-license?)])

        (finally
          (.removeEventListener js/window "scroll" handle-scroll)
          (rf/dispatch [:guardrails->clear-active-guardrail]))))))
