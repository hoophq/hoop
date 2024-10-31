(ns webapp.guardrails.main
  (:require [re-frame.core :as rf]
            ["@radix-ui/themes" :refer [Text]]
            [webapp.components.headings :as h]))

(defn panel []
  (let [guardrails-rules-list (rf/subscribe [:guardrails->list])]
    ;; (rf/dispatch [:guardrails->get-all])
    (fn []
      [:div
       [:header
       [h/h2 "Guardrails" {:class "mb-6"}]
       [:p "Create custom rules to guide and protect usage within your connections"]]
       [:div "iiirraaa"]])))

