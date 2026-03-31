(ns webapp.features.machine-identities.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text]]
   [re-frame.core :as rf]
   [webapp.components.loaders :as loaders]
   [webapp.features.machine-identities.views.empty-state :as empty-state]
   [webapp.features.machine-identities.views.identity-list :as identity-list]))

(defn main []
  (let [identities (rf/subscribe [:machine-identities/identities])
        status (rf/subscribe [:machine-identities/status])]

    (rf/dispatch [:machine-identities/list])

    (fn []
      (let [has-identities? (and @identities (seq @identities))
            loading? (= :loading @status)]

        (cond
          loading?
          [:> Box {:class "bg-gray-1 h-full"}
           [:> Flex {:direction "column" :justify "center" :align "center" :height "100%"}
            [loaders/simple-loader]]]

          :else
          [:> Box {:class "bg-gray-1 p-radix-7 min-h-full h-max"}
           [:> Flex {:direction "column" :gap "6" :class "h-full"}

            [:> Flex {:justify "between" :align "center" :class "mb-6"}
             [:> Box
              [:> Heading {:size "8" :weight "bold" :as "h1"}
               "Machine Identities"]
              [:> Text {:size "5" :class "text-[--gray-11]"}
               "Create an identity for services or applications to securely access resources."]]
             (when has-identities?
               [:> Button {:size "3"
                           :onClick #(rf/dispatch [:navigate :machine-identities-new])}
                "Create new Machine Identity"])]

            (if (not has-identities?)
              [empty-state/main]
              [identity-list/main])]])))))
