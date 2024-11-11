(ns webapp.guardrails.create-update-main
  (:require
   [re-frame.core :as rf]
   [reagent.core :as r]
   ["@radix-ui/themes" :refer [Box Grid]]
   [webapp.components.loaders :as loaders]
   [webapp.guardrails.helpers :as helpers]
   [webapp.guardrails.form-header :as form-header]
   [webapp.guardrails.basic-info :as basic-info]
   [webapp.guardrails.rules-table :as rules-table]))

(defn guardrail-form [form-type guardrails scroll-pos]
  (let [state (helpers/create-form-state guardrails)
        handlers (helpers/create-form-handlers state)]
    (fn []
      [:> Box {:class "min-h-screen bg-gray-1"}
       [form-header/main
        {:form-type form-type
         :id @(:id state)
         :scroll-pos scroll-pos
         :on-save #(let [data {:id @(:id state)
                               :name @(:name state)
                               :description @(:description state)
                               :input @(:input state)
                               :output @(:output state)}]
                     (if (= :edit form-type)
                       (rf/dispatch [:guardrails->update-by-id data])
                       (rf/dispatch [:guardrails->create data])))}]

       [:> Box {:p "7" :class "space-y-radix-9"}
        [basic-info/main
         {:name (:name state)
          :description (:description state)
          :on-name-change #(reset! (:name state) %)
          :on-description-change #(reset! (:description state) %)}]

        ;; Rules section
        [:> Grid {:columns "7" :gap "7"}
         [:> Box {:grid-column "span 2 / span 2"}
          [:h3.text-lg.font-semibold.mt-8 "Configure rules"]
          [:p.text-sm.text-gray-500.mb-4
           "Setup rules with Presets or Custom regular expression scripts."]]

         [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
          ;; Input Rules
          [rules-table/main
           (merge
            {:title "Input rules"
             :state (:input state)
             :words-state (:input-words state)
             :pattern-state (:input-pattern state)
             :select-state (:input-select state)}
            handlers)]

          ;; Output Rules
          [rules-table/main
           (merge
            {:title "Output rules"
             :state (:output state)
             :words-state (:output-words state)
             :pattern-state (:output-pattern state)
             :select-state (:output-select state)}
            handlers)]]]]])))

(defn- loading []
  [:div {:class "flex items-center justify-center rounded-lg border bg-white h-full"}
   [:div {:class "flex items-center justify-center h-full"}
    [loaders/simple-loader]]])

(defn main [form-type]
  (let [guardrails->active-guardrail (rf/subscribe [:guardrails->active-guardrail])
        scroll-pos (r/atom 0)]
    (fn []
      (r/with-let [handle-scroll #(reset! scroll-pos (.-scrollY js/window))]
        (.addEventListener js/window "scroll" handle-scroll)
        (finally
          (.removeEventListener js/window "scroll" handle-scroll)))
      (if (= :loading (:status @guardrails->active-guardrail))
        [loading]
        [guardrail-form form-type (:data @guardrails->active-guardrail) scroll-pos]))))
