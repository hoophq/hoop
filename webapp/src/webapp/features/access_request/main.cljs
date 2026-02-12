(ns webapp.features.access-request.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text Heading]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.features.access-request.views.free-license-callout :refer [free-license-callout]]
   [webapp.features.access-request.views.empty-state :as empty-state]
   [webapp.features.access-request.views.rule-list :as rule-list]
   [webapp.features.promotion :as promotion]))

(defn main []
  (let [rules (rf/subscribe [:access-request/rules])
        status (rf/subscribe [:access-request/status])
        current-user (rf/subscribe [:users->current-user])
        promotion-seen (r/atom (.getItem (.-localStorage js/window) "access-request-promotion-seen"))]

    (rf/dispatch [:access-request/list-rules])

    (fn []
      (let [has-rules? (and @rules (seq @rules))
            loading? (= :loading @status)
            free-license? (get-in @current-user [:data :free-license?])
            rules-count (count (or @rules []))
            limit-reached? (and free-license? (>= rules-count 1))]

        (cond
          loading?
          [:> Box {:class "bg-gray-1 h-full"}
           [:> Flex {:direction "column" :justify "center" :align "center" :height "100%"}
            [loaders/simple-loader]]]

          (and (not has-rules?) (not @promotion-seen))
          [:> Box {:class "bg-gray-1 h-full"}
           [promotion/access-request-promotion {:mode :empty-state
                                                :on-promotion-seen (fn []
                                                                     (.setItem (.-localStorage js/window) "access-request-promotion-seen" "true")
                                                                     (reset! promotion-seen "true"))}]]

          :else
          [:> Box {:class "bg-gray-1 p-radix-7 min-h-full h-max"}
           [:> Flex {:direction "column" :gap "6" :class "h-full"}

            [:> Flex {:justify "between" :align "center" :class "mb-6"}
             [:> Box
              [:> Heading {:size "8" :weight "bold" :as "h1"}
               "Access Request"]
              [:> Text {:size "5" :class "text-[--gray-11]"}
               "Create secure access request rules for your resources"]]
             (when has-rules?
               [:> Button {:size "3"
                           :onClick #(if limit-reached?
                                       (rf/dispatch [:navigate :upgrade-plan])
                                       (rf/dispatch [:navigate :access-request-new]))}
                "Create new Access Request rule"])]

            (when free-license?
              [free-license-callout])

            (if (not has-rules?)
              [empty-state/main]
              [rule-list/main])]])))))
