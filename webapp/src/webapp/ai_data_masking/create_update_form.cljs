(ns webapp.ai-data-masking.create-update-form
  (:require
   ["@radix-ui/themes" :refer [Box Flex Grid Heading Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.components.multiselect :as multiselect]
   [webapp.ai-data-masking.basic-info :as basic-info]
   [webapp.ai-data-masking.connections-section :as connections-section]
   [webapp.ai-data-masking.form-header :as form-header]
   [webapp.ai-data-masking.helpers :as helpers]
   [webapp.ai-data-masking.rules-table :as rules-table]))

(defn ai-data-masking-form [form-type ai-data-masking scroll-pos]
  (let [state (helpers/create-form-state ai-data-masking)
        handlers (helpers/create-form-handlers state)
        submitting? (rf/subscribe [:ai-data-masking->submitting?])
        user (rf/subscribe [:users->current-user])
        attributes-data (rf/subscribe [:attributes/list-data])]
    (fn []
      (let [free-license? (-> @user :data :free-license?)
            all-attributes (or @attributes-data [])]
        [:> Box {:class "min-h-screen bg-gray-1"}
         [:form {:id "ai-data-masking-form"
                 :on-submit (fn [e]
                              (.preventDefault e)
                              (let [data (helpers/prepare-payload state)]
                                (if (= :edit form-type)
                                  (rf/dispatch [:ai-data-masking->update-by-id data])
                                  (rf/dispatch [:ai-data-masking->create data]))))}

          [form-header/main
           {:form-type form-type
            :id @(:id state)
            :scroll-pos scroll-pos
            :loading? @submitting?}]

          [:> Box {:p "7" :class "space-y-radix-9"}
           [basic-info/main
            {:name (:name state)
             :description (:description state)
             :score_threshold (:score_threshold state)
             :on-name-change #(reset! (:name state) %)
             :on-description-change #(reset! (:description state) %)
             :on-score-threshold-change #(reset! (:score_threshold state) %)}]

           ;; Connections section
           [connections-section/main
            {:connection-ids (:connection_ids state)
             :on-connections-change (:on-connections-change handlers)}] 

           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
              "Attribute configuration"]
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "Select which Attributes to apply this configuration."]]
            [:> Box {:grid-column "span 5 / span 5"}
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

           ;; Output rules section
           [:> Flex {:direction "column" :gap "5"}
            [:> Box
             [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
              "Output rules"]]

            [:> Box {:class "space-y-radix-7"}
             [rules-table/main
              (merge
               {:state (:rules state)
                :select-state (:rules-select-state state)
                :free-license? free-license?}
               (select-keys handlers
                            [:on-rule-field-change
                             :on-rule-select
                             :on-toggle-rules-select
                             :on-toggle-all-rules
                             :on-rules-delete
                             :on-rule-add]))]]]]]]))))

(defn- loading []
  [:div {:class "flex items-center justify-center rounded-lg border bg-white h-full"}
   [:div {:class "flex items-center justify-center h-full"}
    [loaders/simple-loader]]])

(defn main [form-type]
  (let [ai-data-masking (rf/subscribe [:ai-data-masking->active-rule])
        scroll-pos (r/atom 0)]

    (rf/dispatch [:attributes/list])

    (fn []
      (r/with-let [handle-scroll #(reset! scroll-pos (.-scrollY js/window))]
        (.addEventListener js/window "scroll" handle-scroll)

        (if (= :loading (:status @ai-data-masking))
          [loading]
          [ai-data-masking-form form-type (:data @ai-data-masking) scroll-pos])
        
        (finally
          (.removeEventListener js/window "scroll" handle-scroll)
          (rf/dispatch [:ai-data-masking->clear-active-rule]))))))
