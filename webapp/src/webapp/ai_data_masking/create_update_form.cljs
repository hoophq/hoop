(ns webapp.ai-data-masking.create-update-form
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.ai-data-masking.basic-info :as basic-info]
   [webapp.ai-data-masking.connections-section :as connections-section]
   [webapp.ai-data-masking.form-header :as form-header]
   [webapp.ai-data-masking.helpers :as helpers]
   [webapp.ai-data-masking.rules-table :as rules-table]))

(defn ai-data-masking-form [form-type ai-data-masking scroll-pos]
  (let [state (helpers/create-form-state ai-data-masking)
        handlers (helpers/create-form-handlers state)
        submitting? (rf/subscribe [:ai-data-masking->submitting?])]
    (fn []
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

         ;; Output rules section
         [:> Flex {:direction "column" :gap "5"}
          [:> Box
           [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
            "Output rules"]]

          [:> Box {:class "space-y-radix-7"}
           [rules-table/main
            (merge
             {:state (:rules state)
              :select-state (:rules-select-state state)}
             (select-keys handlers
                          [:on-rule-field-change
                           :on-rule-select
                           :on-toggle-rules-select
                           :on-toggle-all-rules
                           :on-rules-delete
                           :on-rule-add]))]]]]]])))

(defn- loading []
  [:div {:class "flex items-center justify-center rounded-lg border bg-white h-full"}
   [:div {:class "flex items-center justify-center h-full"}
    [loaders/simple-loader]]])

(defn main [form-type]
  (let [ai-data-masking (rf/subscribe [:ai-data-masking->active-rule])
        scroll-pos (r/atom 0)]

    (fn []
      (r/with-let [handle-scroll #(reset! scroll-pos (.-scrollY js/window))]
        (.addEventListener js/window "scroll" handle-scroll)
        
        (if (= :loading (:status @ai-data-masking))
          [loading]
          [ai-data-masking-form form-type (:data @ai-data-masking) scroll-pos])
        
        (finally
          (.removeEventListener js/window "scroll" handle-scroll)
          (rf/dispatch [:ai-data-masking->clear-active-rule]))))))
